package main

import (
	"bytes"
	"context"
	"crypto/sha256"

	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"golang.org/x/crypto/ssh"
)

const remoteControl = "/home/labbit/lbt145"

func observeBootstrapDelete(ctx context.Context) error {
	actual := servers.Get(ctx, compute, state.ServerID)
	fmt.Println("Bootstrap exact server GET:", safe(actual.Err))
	pages, e := servers.List(compute, servers.ListOpts{}).AllPages(ctx)
	if e != nil {
		return errors.New("bootstrap server inventory unavailable")
	}
	items, e := servers.ExtractServers(pages)
	if e != nil {
		return errors.New("bootstrap server inventory invalid")
	}
	fmt.Println("Complete server inventory count:", len(items))
	for _, item := range items {
		if item.ID == state.ServerID {
			fmt.Println("Known bootstrap inventory state:", item.Status)
		}
	}
	volumePages, e := volumes.List(volume, volumes.ListOpts{}).AllPages(ctx)
	if e != nil {
		return errors.New("bootstrap volume inventory unavailable")
	}
	vs, e := volumes.ExtractVolumes(volumePages)
	if e != nil {
		return errors.New("bootstrap volume inventory invalid")
	}
	for _, v := range vs {
		for _, id := range state.VolumeIDs {
			if v.ID == id {
				match := false
				for _, a := range v.Attachments {
					match = match || a.ServerID == state.ServerID
				}
				fmt.Printf("Bootstrap root inventory status=%s attachments=%d exact-server-attachment=%t\n", v.Status, len(v.Attachments), match)
			}
		}
	}
	for _, id := range state.VolumeIDs {
		v, e := volumes.Get(ctx, volume, id).Extract()
		if e != nil {
			fmt.Println("Bootstrap root GET:", safe(e))
			continue
		}
		owned := false
		for _, a := range v.Attachments {
			owned = owned || a.ServerID == state.ServerID
		}
		fmt.Printf("Bootstrap root status=%s attachments=%d exact-server-attachment=%t\n", v.Status, len(v.Attachments), owned)
	}
	return nil
}

func recordOwnedResidual(ctx context.Context) error {
	b, e := os.ReadFile(filepath.Join(filepath.Dir(ledgerPath), "ktcloud-control-ledger.json"))
	if e != nil {
		return errors.New("Lab ownership ledger unavailable")
	}
	var journal struct {
		Resources []protocol.ProviderResourceResult `json:"resources"`
	}
	if json.Unmarshal(b, &journal) != nil {
		return errors.New("Lab ownership ledger invalid")
	}
	tiers, e := readTiers(ctx)
	if e != nil {
		return e
	}
	policies, e := nsmList[policy](ctx, "firewall/policy")
	if e != nil {
		return e
	}
	forwards, e := nsmList[forwarding](ctx, "portforwarding")
	if e != nil {
		return e
	}
	publics, e := nsmList[publicIP](ctx, "publicIp")
	if e != nil {
		return e
	}
	counts := map[string]int{"LAB_KT_TIER": 0, "LAB_KT_FIREWALL_POLICY": 0, "LAB_SERVER": 0, "LAB_VOLUME": 0, "BOOTSTRAP_SERVER": 0, "BOOTSTRAP_VOLUME": 0, "BOOTSTRAP_KEYPAIR": 0, "BOOTSTRAP_KT_FIREWALL_POLICY": 0, "BOOTSTRAP_PUBLIC_IP": 0, "BOOTSTRAP_PORT_FORWARD": 0}
	for _, r := range journal.Resources {
		present := false
		switch r.ResourceType {
		case "KT_TIER":
			for _, t := range tiers {
				present = present || t.ID == r.ProviderID
			}
		case "KT_FIREWALL_POLICY":
			for _, p := range policies {
				present = present || p.ID == r.ProviderID
			}
		case "SERVER":
			result := servers.Get(ctx, compute, r.ProviderID)
			if result.Err != nil && !gophercloud.ResponseCodeIs(result.Err, 404) {
				return errors.New("Lab server residual unknown")
			}
			present = result.Err == nil
		case "VOLUME":
			result := volumes.Get(ctx, volume, r.ProviderID)
			if result.Err != nil && !gophercloud.ResponseCodeIs(result.Err, 404) {
				return errors.New("Lab volume residual unknown")
			}
			present = result.Err == nil
		default:
			return errors.New("unsupported ledger resource")
		}
		if present {
			counts["LAB_"+r.ResourceType]++
		}
	}
	result := servers.Get(ctx, compute, state.ServerID)
	if result.Err != nil && !gophercloud.ResponseCodeIs(result.Err, 404) {
		return errors.New("bootstrap server residual unknown")
	}
	if result.Err == nil {
		counts["BOOTSTRAP_SERVER"]++
	}
	for _, id := range state.VolumeIDs {
		result := volumes.Get(ctx, volume, id)
		if result.Err != nil && !gophercloud.ResponseCodeIs(result.Err, 404) {
			return errors.New("bootstrap volume residual unknown")
		}
		if result.Err == nil {
			counts["BOOTSTRAP_VOLUME"]++
		}
	}
	key := keypairs.Get(ctx, compute, state.KeyName, nil)
	if key.Err != nil && !gophercloud.ResponseCodeIs(key.Err, 404) {
		return errors.New("bootstrap key residual unknown")
	}
	if key.Err == nil {
		counts["BOOTSTRAP_KEYPAIR"]++
	}
	for _, id := range state.PolicyIDs {
		for _, p := range policies {
			if p.ID == id {
				counts["BOOTSTRAP_KT_FIREWALL_POLICY"]++
			}
		}
	}
	for _, p := range publics {
		if p.ID == state.PublicID {
			counts["BOOTSTRAP_PUBLIC_IP"]++
		}
	}
	for _, f := range forwards {
		if f.ID == state.ForwardID {
			counts["BOOTSTRAP_PORT_FORWARD"]++
		}
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	report := struct {
		At                   string         `json:"at"`
		Counts               map[string]int `json:"ownedResourceCounts"`
		Total                int            `json:"ownedResidualTotal"`
		BasicTierCount       int            `json:"basicTierCount"`
		ExternalNetworkCount int            `json:"externalNetworkEntryCount"`
		NetworkEntryCount    int            `json:"projectNetworkEntryCount"`
		Status               string         `json:"status"`
	}{At: time.Now().UTC().Format(time.RFC3339), Counts: counts, Total: total, NetworkEntryCount: len(tiers), Status: "INCOMPLETE_RESIDUAL_NONZERO"}
	for _, item := range tiers {
		if (item.Name == "DMZ" && item.CIDR == "172.25.0.0/24") || (item.Name == "Private" && item.CIDR == "172.25.1.0/24") {
			report.BasicTierCount++
		}
		if item.Name == "external" && item.CIDR == "" {
			report.ExternalNetworkCount++
		}
	}
	if total == 0 {
		report.Status = "ALL_TRACKED_IDS_CONFIRMED_ABSENT"
	}
	b, e = json.MarshalIndent(report, "", "  ")
	if e != nil {
		return errors.New("residual report encode unavailable")
	}
	if os.WriteFile(filepath.Join(filepath.Dir(ledgerPath), "ktcloud-residual-current.json"), append(b, '\n'), 0600) != nil {
		return errors.New("residual report save unavailable")
	}
	fmt.Println(string(b))
	return nil
}

func inspectTierFilters(ctx context.Context) error {
	b, e := os.ReadFile(filepath.Join(filepath.Dir(ledgerPath), "ktcloud-control-ledger.json"))
	if e != nil {
		return errors.New("known-ID ledger unavailable")
	}
	var journal struct {
		Resources []protocol.ProviderResourceResult `json:"resources"`
	}
	if json.Unmarshal(b, &journal) != nil {
		return errors.New("known-ID ledger invalid")
	}
	for _, r := range journal.Resources {
		if r.ResourceType != "KT_TIER" {
			continue
		}
		var result struct {
			Status int    `json:"httpStatus"`
			Data   []tier `json:"data"`
		}
		_, e := nsm.Get(ctx, nsm.ServiceURL("network")+"?networkType=ALL&networkId="+r.ProviderID+"&size=2000", &result, nil)
		if e != nil {
			return fmt.Errorf("filtered inventory: %s", safe(e))
		}
		if result.Status != 200 || result.Data == nil {
			return errors.New("filtered inventory malformed")
		}
		exact := 0
		for _, item := range result.Data {
			if item.ID == r.ProviderID {
				exact++
				fmt.Printf("Known Tier ownership display: name=%s cidr=%s\n", item.Name, item.CIDR)
			}
		}
		fmt.Printf("Known Tier %s filtered inventory: count=%d exact=%d\n", r.ProviderID, len(result.Data), exact)
	}
	return nil
}

func cleanupControlTiers(ctx context.Context) error {
	b, e := os.ReadFile(filepath.Join(filepath.Dir(ledgerPath), "ktcloud-control-ledger.json"))
	if e != nil {
		return errors.New("Control ledger unavailable")
	}
	var journal struct {
		Resources []protocol.ProviderResourceResult `json:"resources"`
	}
	if json.Unmarshal(b, &journal) != nil {
		return errors.New("Control ledger invalid")
	}
	for _, r := range journal.Resources {
		if r.ResourceType == "SERVER" {
			if result := servers.Get(ctx, compute, r.ProviderID); !gophercloud.ResponseCodeIs(result.Err, 404) {
				return errors.New("actual VM 404 is required before Tier cleanup")
			}
		}
		if r.ResourceType == "VOLUME" {
			if result := volumes.Get(ctx, volume, r.ProviderID); !gophercloud.ResponseCodeIs(result.Err, 404) {
				return errors.New("actual volume 404 is required before Tier cleanup")
			}
		}
	}
	inventory, e := readTiers(ctx)
	if e != nil {
		return e
	}
	for _, r := range journal.Resources {
		if r.ResourceType != "KT_TIER" {
			continue
		}
		deleteID := r.ProviderID
		found := false
		for _, t := range inventory {
			if t.ID == r.ProviderID {
				if t.ID == state.TierID || t.Name == "DMZ" || t.Name == "Private" {
					return errors.New("shared Tier refused")
				}
				found = true
				if os.Getenv("LBT145_BOOTSTRAP_PHASE") == "tier-ref-cleanup-diagnostic" {
					if t.Ref == "" || t.Ref == t.ID {
						return errors.New("verified distinct physical ref unavailable")
					}
					deleteID = t.Ref
				}
			}
		}
		if !found {
			continue
		}
		legacy := os.Getenv("LBT145_BOOTSTRAP_PHASE") == "tier-legacy-cleanup-diagnostic"
		prefix := "ktcloud-tier-cleanup-"
		if legacy {
			prefix = "ktcloud-tier-legacy-cleanup-"
		}
		if os.Getenv("LBT145_BOOTSTRAP_PHASE") == "tier-post-bootstrap-cleanup" {
			if !state.Deleted {
				return errors.New("bootstrap confirmed cleanup is required")
			}
			pp, e := nsmList[policy](ctx, "firewall/policy")
			if e != nil || len(pp) != 0 {
				return errors.New("complete empty policy inventory required")
			}
			pages, e := servers.List(compute, servers.ListOpts{}).AllPages(ctx)
			if e != nil {
				return errors.New("server inventory unavailable")
			}
			items, e := servers.ExtractServers(pages)
			if e != nil || len(items) != 0 {
				return errors.New("complete empty server inventory required")
			}
			prefix = "ktcloud-tier-post-bootstrap-cleanup-"
		}
		path := filepath.Join(filepath.Dir(ledgerPath), prefix+deleteID+".json")
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			continue
		}
		f.Write([]byte("{\"stage\":\"RECONCILED_PRESENT_DELETE_ATTEMPTED\"}"))
		f.Close()
		var receipt struct {
			Status int    `json:"httpStatus"`
			JobID  string `json:"jobId"`
		}
		target := nsm.ServiceURL("network", deleteID)
		if legacy {
			target = base + "nc/Network/" + deleteID
		}
		response, e := nsm.Delete(ctx, target, &gophercloud.RequestOpts{OkCodes: []int{200, 202, 204}, JSONResponse: &receipt})
		if e != nil {
			var responseError gophercloud.ErrUnexpectedResponseCode
			if errors.As(e, &responseError) {
				os.WriteFile(path+".error", responseError.Body, 0600)
				var fields map[string]any
				json.Unmarshal(responseError.Body, &fields)
				for key := range fields {
					fmt.Println("Tier delete error field:", key)
				}
				for _, key := range []string{"httpStatus", "status", "code", "error", "message"} {
					if value, ok := fields[key]; ok {
						if key == "message" {
							lower := strings.ToLower(fmt.Sprint(value))
							for _, word := range []string{"not found", "notfound", "network", "no static resource", "method", "url", "permission", "권한", "요청", "존재", "없", "삭제", "인증"} {
								if strings.Contains(lower, word) {
									fmt.Println("Tier delete safe message classification:", word)
								}
							}
						} else {
							fmt.Println("Tier delete error", key, value)
						}
					}
				}
			}
			return fmt.Errorf("reconciled Tier delete receipt: %s", safe(e))
		}
		b, _ = json.Marshal(receipt)
		os.WriteFile(path, b, 0600)
		fmt.Println("Reconciled owned Tier delete: HTTP", response.StatusCode, "body status", receipt.Status, "job recorded", receipt.JobID != "")
		if receipt.JobID != "" {
			for {
				var job struct {
					Status string          `json:"jobStatus"`
					Data   json.RawMessage `json:"data"`
				}
				_, e = nsm.Get(ctx, nsm.ServiceURL("job/status", receipt.JobID), &job, nil)
				if e != nil {
					return errors.New("Tier delete job observation unavailable")
				}
				fmt.Println("Owned Tier delete job state:", job.Status)
				if job.Status == "SUCCESS" {
					break
				}
				if job.Status == "FAILURE" || job.Status == "FAILED" {
					var fields map[string]any
					json.Unmarshal(job.Data, &fields)
					for key := range fields {
						fmt.Println("Tier job failure data field:", key)
					}
					lower := strings.ToLower(string(job.Data))
					for _, keyword := range []string{"port", "network", "in use", "in-use", "unable", "conflict", "vm", "policy", "firewall", "failed", "neutron", "volume", "delete", "400", "404", "409", "500"} {
						if strings.Contains(lower, keyword) {
							fmt.Println("Tier job safe failure keyword:", keyword)
						}
					}
					return errors.New("Tier delete job failed; raw data omitted")
				}
				select {
				case <-ctx.Done():
					return errors.New("Tier job observation timeout")
				case <-time.After(3 * time.Second):
				}
			}
		}
	}
	return nil
}

func purgeControlVM(ctx context.Context) error {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(ledgerPath), "ktcloud-control-ledger.json"))
	if err != nil {
		return errors.New("confirmed Control ledger unavailable")
	}
	var journal struct {
		Resources []protocol.ProviderResourceResult `json:"resources"`
	}
	if json.Unmarshal(b, &journal) != nil {
		return errors.New("confirmed Control ledger invalid")
	}
	path := filepath.Join(filepath.Dir(ledgerPath), "ktcloud-lab-force-delete-attempt.json")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("force delete already attempted; inspect without retry")
	}
	f.Write(b)
	f.Close()
	for _, r := range journal.Resources {
		if r.ResourceType == "SERVER" {
			err = servers.ForceDelete(ctx, compute, r.ProviderID).ExtractErr()
			fmt.Println("Known fixture force-delete receipt:", safe(err))
			return err
		}
	}
	return errors.New("confirmed fixture VM identity unavailable")
}

func upload(client *ssh.Client, name string, reader io.Reader) error {
	session, err := client.NewSession()
	if err != nil {
		return errors.New("upload session unavailable")
	}
	defer session.Close()
	session.Stdin = reader
	// Every name passed here is a trusted literal, never a cloud response.
	if session.Run("umask 077; cat > "+remoteControl+"/"+name) != nil {
		return errors.New("protected SSH upload failed")
	}
	return nil
}

func inspectControlReality(ctx context.Context) error {
	bootstrap, err := servers.Get(ctx, compute, state.ServerID).Extract()
	if err == nil && bootstrap != nil {
		fmt.Printf("Safe Nova detail flavor fields: %#v\n", bootstrap.Flavor)
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(ledgerPath), "ktcloud-control-ledger.json"))
	if err != nil {
		return errors.New("Control ledger unavailable")
	}
	var journal struct {
		Resources []protocol.ProviderResourceResult `json:"resources"`
	}
	if json.Unmarshal(b, &journal) != nil {
		return errors.New("Control ledger invalid")
	}
	for _, status := range []string{"SOFT_DELETED", "DELETED"} {
		pages, e := servers.List(compute, servers.ListOpts{Status: status}).AllPages(ctx)
		if e != nil {
			fmt.Println("Soft-delete inventory:", status, safe(e))
			continue
		}
		items, e := servers.ExtractServers(pages)
		if e != nil {
			return errors.New("soft delete inventory invalid")
		}
		fmt.Println("Soft-delete inventory:", status, "count", len(items))
		for _, item := range items {
			for _, r := range journal.Resources {
				if r.ResourceType == "SERVER" && r.ProviderID == item.ID {
					fmt.Println("Known Lab VM filtered state:", item.Status, item.VmState)
				}
			}
		}
	}
	for _, query := range []string{"deleted=true", "soft_deleted=true"} {
		var result struct {
			Servers []servers.Server `json:"servers"`
		}
		_, e := compute.Get(ctx, compute.ServiceURL("servers/detail")+"?"+query, &result, nil)
		fmt.Println("Deletion inventory query:", query, "result", safe(e), "count", len(result.Servers))
		for _, item := range result.Servers {
			for _, r := range journal.Resources {
				if r.ResourceType == "SERVER" && r.ProviderID == item.ID {
					fmt.Println("Known Lab VM deletion state:", item.Status, item.VmState)
				}
			}
		}
	}
	tiers, err := readTiers(ctx)
	if err != nil {
		return err
	}
	var details struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	_, err = nsm.Get(ctx, nsm.ServiceURL("network")+"?page=1&size=2000&networkType=ALL", &details, nil)
	if err != nil {
		return err
	}
	for _, raw := range details.Data {
		var id string
		json.Unmarshal(raw["networkId"], &id)
		for _, r := range journal.Resources {
			if r.ResourceType == "KT_TIER" && r.ProviderID == id {
				keys := []string{}
				for key := range raw {
					keys = append(keys, key)
				}
				fmt.Println("Known Lab Tier field names:", keys)
				for _, key := range []string{"status", "networkStatus", "jobId", "vmCount", "serverCount", "isShared", "shared", "refId", "mainNetworkId", "isPrivateSubnet", "type", "isAllocate"} {
					if value, ok := raw[key]; ok {
						fmt.Println("Known Lab Tier", key, string(value))
					}
				}
				var project string
				json.Unmarshal(raw["projectId"], &project)
				fmt.Println("Known Lab Tier project matches token project:", project == state.Project)
			}
		}
	}
	policies, err := nsmList[policy](ctx, "firewall/policy")
	if err != nil {
		return err
	}
	for _, r := range journal.Resources {
		switch r.ResourceType {
		case "SERVER":
			result := servers.Get(ctx, compute, r.ProviderID)
			if result.Err != nil {
				fmt.Println("Known Lab server reality:", safe(result.Err))
				continue
			}
			if body, ok := result.Body.(map[string]any); ok {
				for key, value := range body {
					if key == "server" {
						fmt.Println("Known Lab server reality: server envelope exists")
						continue
					}
					fmt.Println("Known Lab server response envelope:", key)
					if fault, ok := value.(map[string]any); ok {
						fmt.Println("Known Lab server fault code:", fault["code"])
					}
				}
			}
		case "VOLUME":
			result := volumes.Get(ctx, volume, r.ProviderID)
			if v, e := result.Extract(); e == nil && v != nil {
				fmt.Println("Known Lab root volume status:", v.Status, "attachments:", len(v.Attachments))
			}
			if result.Err != nil {
				fmt.Println("Known Lab volume reality:", safe(result.Err))
				continue
			}
			if body, ok := result.Body.(map[string]any); ok {
				for key, value := range body {
					if key == "volume" {
						fmt.Println("Known Lab root volume reality: volume envelope exists")
						continue
					}
					fmt.Println("Known Lab volume response envelope:", key)
					if fault, ok := value.(map[string]any); ok {
						fmt.Println("Known Lab volume fault code:", fault["code"])
					}
				}
			}
		case "KT_TIER":
			found := false
			for _, v := range tiers {
				found = found || v.ID == r.ProviderID
			}
			fmt.Println("Known Lab Tier present:", found)
		case "KT_FIREWALL_POLICY":
			found := false
			for _, v := range policies {
				found = found || v.ID == r.ProviderID
			}
			fmt.Println("Known Lab firewall present:", found)
		}
	}
	return nil
}
func deployControl(ctx context.Context) error {
	client, err := connectSSH(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return errors.New("deploy session unavailable")
	}
	err = session.Run("umask 077; mkdir -p " + remoteControl + "; chmod 700 " + remoteControl)
	session.Close()
	if err != nil {
		return errors.New("protected remote directory unavailable")
	}
	localDir := filepath.Dir(ledgerPath)
	for _, item := range []struct{ name, path string }{
		{"labbit-connector", filepath.Join(localDir, "labbit-connector-ktcloud")},
		{"control-harness", filepath.Join(localDir, "lbt145-control-linux")},
		{"labbit-workspace-sftp", filepath.Join(localDir, "labbit-workspace-sftp")},
		{"clouds.yaml", os.Getenv("LABBIT_PROVIDER_CONFIG_FILE")},
		{"lab-key.pem", keyPath},
	} {
		f, e := os.Open(item.path)
		if e != nil {
			return errors.New("local deploy input unavailable")
		}
		e = upload(client, item.name, f)
		f.Close()
		if e != nil {
			return e
		}
	}
	snapshot := protocol.CreationSnapshot{ProviderConnectionID: "lbt145-ktcloud", WorkspaceVMKey: "workspace", InternetOutbound: false,
		VMs: []protocol.ResolvedVmSpec{{VMKey: "workspace", Role: "workspace", ImageID: state.Image, FlavorID: state.Flavor,
			FlavorSpec: &protocol.ResolvedFlavorSpec{VCPUs: int64(state.CPU), RAMMiB: int64(state.RAM), DiskGiB: int64(state.Disk)}}}}
	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return errors.New("snapshot encode unavailable")
	}
	if err = upload(client, "snapshot.json", bytes.NewReader(b)); err != nil {
		return err
	}
	vars := map[string]string{
		"LABBIT_PROVIDER_CONFIG_FILE": remoteControl + "/clouds.yaml", "OS_CLOUD": "ktcloud-d1", "OS_PROJECT_ID": state.Project,
		"LABBIT_PROVIDER_CONNECTION_ID": "lbt145-ktcloud", "LABBIT_KTCLOUD_CONNECTOR_TIER_ID": state.TierID,
		"LABBIT_KTCLOUD_MANAGEMENT_CIDR": "172.25.241.0/24", "LABBIT_OPENSTACK_LAB_SUBNET_CIDR": "172.25.240.0/24",
		"LABBIT_OPENSTACK_KEYPAIR_NAME": state.KeyName, "LABBIT_OPENSTACK_SSH_ALLOWED_CIDR": state.PrivateIP + "/32",
		"LABBIT_OPENSTACK_SSH_USERNAME": "ubuntu", "LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE": remoteControl + "/lab-key.pem",
		"LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE": remoteControl + "/lab-known_hosts",
		"LABBIT_KTCLOUD_D1_CONTROL_TEST":        "1", "LBT145_CONTROL_DIRECTORY": remoteControl,
		"LBT145_CONTROL_GENERATIONS":   os.Getenv("LBT145_CONTROL_GENERATIONS"),
		"LBT145_PREMERGE_LIVE_TEST":    "1",
		"LABBIT_KTCLOUD_OP02_TEST":     "1",
		"LABBIT_WORKSPACE_ROOT":        "/var/lib/labbit-workspaces/workspace",
		"LABBIT_WORKSPACE_SFTP_HELPER": "/usr/local/libexec/labbit-workspace-sftp",
	}
	op02Mode, previewMode := fixtureModes(os.Getenv("LBT173_BOOTSTRAP_PREVIEW_SMOKE") == "1")
	transportMode := "0"
	if os.Getenv("LBT175_BOOTSTRAP_PREVIEW_TRANSPORT") == "1" {
		op02Mode, previewMode, transportMode = "0", "0", "1"
	}
	vars["LABBIT_KTCLOUD_OP02_TEST"] = op02Mode
	vars["LABBIT_KTCLOUD_PREVIEW_SMOKE"] = previewMode
	vars["LABBIT_KTCLOUD_PREVIEW_TRANSPORT_TEST"] = transportMode
	var env strings.Builder
	for k, v := range vars {
		if strings.ContainsAny(v, "'\r\n") {
			return errors.New("deployment environment value invalid")
		}
		fmt.Fprintf(&env, "export %s='%s'\n", k, v)
	}
	if err = upload(client, "environment", strings.NewReader(env.String())); err != nil {
		return err
	}
	session, err = client.NewSession()
	if err != nil {
		return errors.New("mode session unavailable")
	}
	defer session.Close()
	if session.Run("chmod 700 "+remoteControl+"/labbit-connector "+remoteControl+"/control-harness ; test -f "+remoteControl+"/lab-known_hosts || (umask 077; touch "+remoteControl+"/lab-known_hosts)") != nil {
		return errors.New("protected executable deployment failed")
	}
	verifySession, err := client.NewSession()
	if err != nil {
		return errors.New("deployed binary verification unavailable")
	}
	output, err := verifySession.Output("sha256sum " + remoteControl + "/labbit-connector " + remoteControl + "/control-harness " + remoteControl + "/labbit-workspace-sftp")
	verifySession.Close()
	fields := strings.Fields(string(output))
	if err != nil || len(fields) != 6 {
		return errors.New("deployed binary digest unavailable")
	}
	for i, name := range []string{"labbit-connector-ktcloud", "lbt145-control-linux", "labbit-workspace-sftp"} {
		input, e := os.ReadFile(filepath.Join(localDir, name))
		if e != nil {
			return errors.New("local binary digest unavailable")
		}
		digest := sha256.Sum256(input)
		if hex.EncodeToString(digest[:]) != fields[i*2] {
			return errors.New("deployed binary digest differs")
		}
		fmt.Printf("Verified deployed %s SHA256: %s\n", name, fields[i*2])
	}
	fmt.Println("Deployment: production Connector and Control harness uploaded through pinned authenticated SSH; remote files protected")
	return nil
}
func runControl(ctx context.Context) error {
	return runControlCommand(ctx, false, false)
}

// Recovery reuses the protected remote ledger. The acceptance harness validates
// all typed KNOWN observations before it can mutate any remaining exact IDs.
// It cannot create a Lab or run OP02 again over the prior failed fixture.
func runRecovery(ctx context.Context, readOnly bool) error {
	return runControlCommand(ctx, true, readOnly)
}

// Preserve the complete failed run before preparing a fresh Lab on the same
// bootstrap host. Current remote ownership is collected again, then every
// recorded Lab ID is queried absent. Bootstrap/shared resources stay in place.
func archiveControl(ctx context.Context) error {
	if err := collectControl(ctx); err != nil {
		return err
	}
	if err := recordOwnedResidual(ctx); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(ledgerPath), "ktcloud-residual-current.json"))
	var residual struct {
		Counts map[string]int `json:"ownedResourceCounts"`
	}
	if err != nil || json.Unmarshal(b, &residual) != nil {
		return errors.New("archive residual unavailable")
	}
	for _, kind := range []string{"LAB_KT_TIER", "LAB_KT_FIREWALL_POLICY", "LAB_SERVER", "LAB_VOLUME"} {
		n, ok := residual.Counts[kind]
		if !ok || n != 0 {
			return errors.New("cannot archive before all owned Lab IDs are absent")
		}
	}
	client, err := connectSSH(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return errors.New("archive session unavailable")
	}
	defer session.Close()
	archive := remoteControl + "-archived-" + fmt.Sprint(time.Now().UnixNano())
	if session.Run("test -d "+remoteControl+" && test ! -e "+archive+" && ! pgrep -f '^"+remoteControl+"/labbit-connector$' >/dev/null && mv "+remoteControl+" "+archive) != nil {
		return errors.New("protected Control archive failed")
	}
	fmt.Println("Protected remote Control directory archived after actual Lab absence; fresh deploy required")
	return nil
}

func controlCommand(recovery, readOnly bool) string {
	mode := ""
	if recovery {
		mode = "export LABBIT_KTCLOUD_OP02_TEST=0 LABBIT_KTCLOUD_PREVIEW_SMOKE=0 LABBIT_KTCLOUD_PREVIEW_TRANSPORT_TEST=0 LBT145_CONTROL_RECOVERY=1 LBT145_CONTROL_RECOVERY_READ_ONLY=0; "
		if readOnly {
			mode = "export LABBIT_KTCLOUD_OP02_TEST=0 LABBIT_KTCLOUD_PREVIEW_SMOKE=0 LABBIT_KTCLOUD_PREVIEW_TRANSPORT_TEST=0 LBT145_CONTROL_RECOVERY=1 LBT145_CONTROL_RECOVERY_READ_ONLY=1; "
		}
	}
	return ". " + remoteControl + "/environment; cd " + remoteControl + "; " + mode + "umask 077; exec timeout --signal=TERM --kill-after=30s 45m ./control-harness"
}

// Existing OP02 remains the default; Preview smoke has its own exclusive gate.
func fixtureModes(preview bool) (op02Mode, previewMode string) {
	if preview {
		return "0", "1"
	}
	return "1", "0"
}

func runControlCommand(ctx context.Context, recovery, readOnly bool) error {
	client, err := connectSSH(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return errors.New("Control execution session unavailable")
	}
	defer session.Close()
	logName := "ktcloud-control-acceptance.log"
	if recovery {
		logName = fmt.Sprintf("ktcloud-control-recovery-%d.log", time.Now().UnixNano())
	}
	file, err := os.OpenFile(filepath.Join(filepath.Dir(ledgerPath), logName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("local Control evidence already exists or unavailable")
	}
	defer file.Close()
	session.Stdout = io.MultiWriter(os.Stdout, file)
	session.Stderr = session.Stdout
	err = session.Run(controlCommand(recovery, readOnly))
	if err != nil {
		return errors.New("real production Control acceptance failed; collect protected ledger before cleanup")
	}
	return nil
}
func collectControl(ctx context.Context) error {
	client, err := connectSSH(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	for _, name := range []string{"control-ledger.json", "connector.log"} {
		session, e := client.NewSession()
		if e != nil {
			return errors.New("collect session unavailable")
		}
		b, e := session.Output("cat " + remoteControl + "/" + name)
		session.Close()
		if e != nil {
			return errors.New("protected remote evidence unavailable")
		}
		if os.WriteFile(filepath.Join(filepath.Dir(ledgerPath), "ktcloud-"+name), b, 0600) != nil {
			return errors.New("protected local evidence save failed")
		}
	}
	fmt.Println("Collection: safe lifecycle ledger and Connector log saved locally; credentials excluded")
	return nil
}

func diagnoseControl(ctx context.Context) error {
	client, err := connectSSH(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	f, err := os.Open(filepath.Join(filepath.Dir(ledgerPath), "lbt145-probe-linux"))
	if err != nil {
		return errors.New("probe binary unavailable")
	}
	err = upload(client, "probe", f)
	f.Close()
	if err != nil {
		return err
	}
	pages, err := servers.List(compute, servers.ListOpts{}).AllPages(ctx)
	if err != nil {
		return errors.New("diagnostic inventory unavailable")
	}
	items, err := servers.ExtractServers(pages)
	if err != nil {
		return errors.New("diagnostic inventory decode unavailable")
	}
	for _, item := range items {
		if item.Metadata["labbit_vm_key"] != "workspace" {
			continue
		}
		fmt.Println("Lab diagnostic: actual server state", item.Status, "attached root volumes", len(item.AttachedVolumes))
		for name, raw := range item.Addresses {
			if !strings.HasSuffix(name, "-management-tier") {
				continue
			}
			b, _ := json.Marshal(raw)
			var addresses []struct {
				IP string `json:"addr"`
			}
			if json.Unmarshal(b, &addresses) != nil {
				continue
			}
			for _, addr := range addresses {
				ip, e := netip.ParseAddr(addr.IP)
				if e != nil || !ip.Is4() {
					continue
				}
				session, e := client.NewSession()
				if e != nil {
					return errors.New("diagnostic SSH session unavailable")
				}
				out, e := session.CombinedOutput(". " + remoteControl + "/environment; chmod 700 " + remoteControl + "/probe; LBT145_PROBE_SERVER_ID='" + item.ID + "' LBT145_PROBE_ADDRESS='" + ip.String() + "' " + remoteControl + "/probe")
				session.Close()
				if os.WriteFile(filepath.Join(filepath.Dir(ledgerPath), "ktcloud-lab-diagnostic.txt"), out, 0600) != nil {
					return errors.New("protected diagnostic save unavailable")
				}
				fmt.Println("Lab diagnostic: SSH command exit success", e == nil, "protected diagnostic saved; no raw output printed")
				fmt.Print(string(out))
			}
		}
	}
	return nil
}
