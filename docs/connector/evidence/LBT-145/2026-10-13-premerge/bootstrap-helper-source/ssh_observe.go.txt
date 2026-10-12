package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"golang.org/x/crypto/ssh"
	"os"
	"path/filepath"
)

func publicKeyEqual(registered string, expected ssh.PublicKey) (bool, error) {
	actual, _, _, _, err := ssh.ParseAuthorizedKey([]byte(registered))
	if err != nil || expected == nil {
		return false, errors.New("registered public key unknown")
	}
	return bytes.Equal(actual.Marshal(), expected.Marshal()), nil
}

func verifyRegisteredKey(ctx context.Context) error {
	b, err := os.ReadFile(keyPath)
	if err != nil {
		return errors.New("protected key unavailable")
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return errors.New("protected key invalid")
	}
	registered, err := keypairs.Get(ctx, compute, state.KeyName, nil).Extract()
	if err != nil || registered == nil {
		return errors.New("registered key read unavailable")
	}
	match, err := publicKeyEqual(registered.PublicKey, signer.PublicKey())
	if err != nil {
		return err
	}
	fmt.Printf("Nova registered public key matches protected local key: %t; name matches: %t\n", match, registered.Name == state.KeyName)
	if !match || registered.Name != state.KeyName {
		return errors.New("registered key mismatch; VM creation refused")
	}
	return nil
}

// Explicit read-only evidence. Candidate IDs never enter an ownership ledger.
func observeLiveSSH(ctx context.Context) error {
	if err := verifyRegisteredKey(ctx); err != nil {
		return err
	}
	if err := collectControl(ctx); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(ledgerPath), "ktcloud-control-ledger.json"))
	var journal struct {
		LabID string `json:"labInstanceId"`
	}
	if err != nil || json.Unmarshal(b, &journal) != nil || journal.LabID == "" {
		return errors.New("exact current Lab identity unavailable")
	}
	bootstrap, err := getServer(ctx, state.ServerID)
	if err != nil {
		return errors.New("bootstrap detail unavailable")
	}
	bootstrapAddressMatches := false
	for name, raw := range bootstrap.Addresses {
		if name != "DMZ" {
			continue
		}
		data, _ := json.Marshal(raw)
		var entries []struct {
			IP   string `json:"addr"`
			Type string `json:"OS-EXT-IPS:type"`
		}
		if json.Unmarshal(data, &entries) != nil {
			return errors.New("bootstrap addresses unknown")
		}
		for _, a := range entries {
			bootstrapAddressMatches = bootstrapAddressMatches || (a.Type == "fixed" && a.IP == state.PrivateIP)
		}
	}
	pages, err := servers.List(compute, servers.ListOpts{}).AllPages(ctx)
	if err != nil {
		return errors.New("server inventory unavailable")
	}
	items, err := servers.ExtractServers(pages)
	if err != nil {
		return errors.New("server inventory invalid")
	}
	tiers, err := readTiers(ctx)
	if err != nil {
		return err
	}
	type iface struct {
		ID string `json:"networkId"`
	}
	type addr struct {
		Name string `json:"name"`
	}
	type service struct {
		Protocol string `json:"protocol"`
		Start    string `json:"startPort"`
		End      string `json:"endPort"`
	}
	type observedPolicy struct {
		ID          string    `json:"policyId"`
		Action      string    `json:"action"`
		Status      string    `json:"status"`
		Source      []iface   `json:"srcInterface"`
		Destination []iface   `json:"dstInterface"`
		SourceIP    []addr    `json:"srcAddress"`
		Services    []service `json:"services"`
	}
	policies, err := nsmList[observedPolicy](ctx, "firewall/policy")
	if err != nil {
		return errors.New("policy inventory unavailable")
	}
	for _, item := range items {
		if item.Metadata["labbit_lab_instance_id"] != journal.LabID || item.Metadata["labbit_vm_key"] != "workspace" || item.TenantID != state.Project {
			continue
		}
		actual, err := getServer(ctx, item.ID)
		if err != nil || actual.Metadata["labbit_lab_instance_id"] != journal.LabID || actual.Metadata["labbit_vm_key"] != "workspace" || actual.Metadata["labbit_generation"] != item.Metadata["labbit_generation"] {
			return errors.New("candidate exact detail unavailable")
		}
		fmt.Printf("Read-only live candidate server=%s generation=%s state=%s keyNameMatches=%t bootstrapFixedIPMatches=%t candidateOwnership=false\n", actual.ID, actual.Metadata["labbit_generation"], actual.Status, actual.KeyName == state.KeyName, bootstrapAddressMatches)
		for _, t := range tiers {
			if t.CIDR != "172.25.241.0/24" {
				continue
			}
			if _, ok := actual.Addresses[t.Name]; !ok {
				continue
			}
			fmt.Printf("Actual Management NIC Tier: NSM=%s physicalRef=%s name=%s cidr=%s\n", t.ID, t.Ref, t.Name, t.CIDR)
			for _, p := range policies {
				src, dst, sourceIP, port := false, false, false, false
				for _, i := range p.Source {
					src = src || i.ID == state.TierID
				}
				for _, i := range p.Destination {
					dst = dst || i.ID == t.ID
				}
				for _, a := range p.SourceIP {
					sourceIP = sourceIP || a.Name == state.PrivateIP+"/32"
				}
				for _, s := range p.Services {
					port = port || (s.Protocol == "TCP" && s.Start == "22" && s.End == "22")
				}
				if dst {
					fmt.Printf("Live Management policy=%s action=%s state=%s sourceTierMatches=%t sourceCIDRMatches=%t tcp22Matches=%t\n", p.ID, p.Action, p.Status, src, sourceIP, port)
				}
			}
		}
	}
	return nil
}
