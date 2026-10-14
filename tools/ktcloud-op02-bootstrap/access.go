package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gophercloud/gophercloud/v2"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type publicIP struct {
	ID string `json:"publicIpId"`
	IP string `json:"publicIp"`
}
type forwarding struct {
	ID       string `json:"portForwardingId"`
	PublicID string `json:"publicIpId"`
	IP       string `json:"mappedIp"`
}
type policy struct {
	ID      string `json:"policyId"`
	Comment string `json:"comment"`
}

func nsmList[T any](ctx context.Context, path string) ([]T, error) {
	var r struct {
		HTTP       int `json:"httpStatus"`
		Data       []T `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	_, e := nsm.Get(ctx, nsm.ServiceURL(path)+"?page=1&size=2000", &r, nil)
	if e != nil {
		return nil, e
	}
	if r.HTTP != 200 || r.Data == nil || r.Pagination.Total != len(r.Data) {
		return nil, errors.New("incomplete NSM inventory")
	}
	return r.Data, nil
}
func nsmCreate(ctx context.Context, path string, payload any) (map[string]json.RawMessage, error) {
	var r map[string]json.RawMessage
	_, e := nsm.Post(ctx, nsm.ServiceURL(path), payload, &r, &gophercloud.RequestOpts{OkCodes: []int{200, 201, 202}})
	if e != nil {
		return nil, e
	}
	var code int
	if json.Unmarshal(r["httpStatus"], &code) != nil || code < 200 || code >= 300 {
		return nil, errors.New("NSM operation did not return a successful receipt")
	}
	return r, nil
}
func waitJob(ctx context.Context, id string) error {
	for {
		var r struct {
			ID    string `json:"jobId"`
			State string `json:"jobStatus"`
		}
		_, e := nsm.Get(ctx, nsm.ServiceURL("job/status", id), &r, nil)
		if e != nil || r.ID != id {
			return errors.New("NSM job observation unavailable")
		}
		if r.State == "SUCCESS" {
			return nil
		}
		if r.State == "FAILED" || r.State == "FAILURE" {
			return errors.New("NSM job failed")
		}
		select {
		case <-ctx.Done():
			return errors.New("NSM job timed out")
		case <-time.After(2 * time.Second):
		}
	}
}
func fixturePolicy(ctx context.Context, suffix string, payload map[string]any) error {
	comment := state.Name + ":" + suffix
	payload["comment"] = comment
	if state.PolicyJobs == nil {
		state.PolicyJobs = map[string]string{}
	}
	if state.PolicyAttempted == nil {
		state.PolicyAttempted = map[string]bool{}
	}
	existing, e := nsmList[policy](ctx, "firewall/policy")
	if e != nil {
		return e
	}
	matches := 0
	for _, p := range existing {
		if p.Comment == comment {
			matches++
			tracked := false
			for _, id := range state.PolicyIDs {
				tracked = tracked || id == p.ID
			}
			if !tracked {
				return errors.New("matching policy is an unowned candidate; inspect without mutation")
			}
		}
	}
	if matches > 1 {
		return errors.New("ambiguous fixture firewall policy")
	}
	if matches == 1 {
		return save("POLICY_" + suffix + "_IDENTITY_CONFIRMED")
	}
	if !state.PolicyAttempted[suffix] {
		state.PolicyAttempted[suffix] = true
		if e = save("POLICY_" + suffix + "_CREATE_ATTEMPTED"); e != nil {
			return e
		}
		r, e := nsmCreate(ctx, "firewall/policy", payload)
		if e != nil {
			return fmt.Errorf("fixture policy create: %s", safe(e))
		}
		var job string
		if json.Unmarshal(r["jobId"], &job) != nil || job == "" {
			return errors.New("policy job identity unavailable")
		}
		state.PolicyJobs[suffix] = job
		state.JobIDs = append(state.JobIDs, job)
		if e = save("POLICY_" + suffix + "_JOB_RECORDED"); e != nil {
			return e
		}
	}
	if state.PolicyJobs[suffix] == "" {
		return errors.New("policy create outcome unknown; inspect without retry")
	}
	if e = waitJob(ctx, state.PolicyJobs[suffix]); e != nil {
		return e
	}
	pp, e := nsmList[policy](ctx, "firewall/policy")
	if e != nil {
		return e
	}
	matches = 0
	for _, p := range pp {
		if p.Comment == comment {
			matches++
			state.PolicyIDs = append(state.PolicyIDs, p.ID)
		}
	}
	if matches != 1 {
		return errors.New("successful job has no unique confirmed policy")
	}
	return save("POLICY_" + suffix + "_CONFIRMED")
}
func access(ctx context.Context) error {
	if state.ServerID == "" || state.PrivateIP == "" || state.ExternalTierID == "" || state.Deleted {
		return errors.New("ACTIVE fixture with known IP and external Tier is required")
	}
	if state.SourceCIDR == "" {
		request, e := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org", nil)
		if e != nil {
			return errors.New("public source request unavailable")
		}
		client := http.Client{Timeout: 15 * time.Second}
		r, e := client.Do(request)
		if e != nil {
			return errors.New("public source IP lookup unavailable")
		}
		b, e := io.ReadAll(io.LimitReader(r.Body, 100))
		r.Body.Close()
		ip, e2 := netip.ParseAddr(strings.TrimSpace(string(b)))
		if e != nil || r.StatusCode != 200 || e2 != nil || !ip.Is4() || !ip.IsGlobalUnicast() {
			return errors.New("public source IP is invalid")
		}
		state.SourceCIDR = ip.String() + "/32"
		if e = save("LOCAL_SSH_SOURCE_CIDR_CONFIRMED"); e != nil {
			return e
		}
	}
	if state.PublicID == "" {
		if state.PublicAttempted || state.Stage == "PUBLIC_IP_CREATE_ATTEMPTED" {
			return errors.New("public IP result unknown; do not recreate")
		}
		ips, e := nsmList[publicIP](ctx, "publicIp")
		if e != nil || len(ips) != 0 {
			return errors.New("expected empty public IP baseline unavailable")
		}
		state.PublicAttempted = true
		if e = save("PUBLIC_IP_CREATE_ATTEMPTED"); e != nil {
			return e
		}
		r, e := nsmCreate(ctx, "publicIp", map[string]any{})
		if e != nil {
			return fmt.Errorf("public IP create: %s", safe(e))
		}
		var data publicIP
		if json.Unmarshal(r["data"], &data) != nil || data.ID == "" {
			return errors.New("public IP receipt identity unavailable")
		}
		state.PublicID = data.ID
		if e = save("PUBLIC_IP_ID_RECORDED"); e != nil {
			return e
		}
	}
	ips, e := nsmList[publicIP](ctx, "publicIp")
	if e != nil {
		return e
	}
	for _, ip := range ips {
		if ip.ID == state.PublicID {
			state.PublicIP = ip.IP
		}
	}
	if state.PublicIP == "" {
		return errors.New("created public IP value unavailable")
	}
	if state.ForwardID == "" {
		if state.ForwardAttempted || state.Stage == "SSH_FORWARD_CREATE_ATTEMPTED" {
			return errors.New("forward outcome unknown; do not recreate")
		}
		state.ForwardAttempted = true
		if e = save("SSH_FORWARD_CREATE_ATTEMPTED"); e != nil {
			return e
		}
		r, e := nsmCreate(ctx, "portforwarding", map[string]any{"publicIpId": state.PublicID, "mappedIp": state.PrivateIP, "protocol": "TCP", "startPrivatePort": "22", "endPrivatePort": "22", "startPublicPort": "22", "endPublicPort": "22"})
		if e != nil {
			return fmt.Errorf("SSH forwarding create: %s", safe(e))
		}
		var data forwarding
		if json.Unmarshal(r["data"], &data) != nil || data.ID == "" {
			return errors.New("forward receipt identity unavailable")
		}
		state.ForwardID = data.ID
		if e = save("SSH_FORWARD_ID_RECORDED"); e != nil {
			return e
		}
	}
	if e = fixturePolicy(ctx, "bootstrap-ssh", map[string]any{"action": true, "protocol": "TCP", "startPort": "22", "endPort": "22", "srcNetwork": []string{state.ExternalTierID}, "dstNetwork": []string{state.TierID}, "srcAddress": []string{state.SourceCIDR}, "portForwardingId": state.ForwardID, "srcNat": false}); e != nil {
		return e
	}
	for _, entry := range []struct{ suffix, protocol, port string }{{"bootstrap-https", "TCP", "443"}, {"bootstrap-dns", "UDP", "53"}} {
		if e = fixturePolicy(ctx, entry.suffix, map[string]any{"action": true, "protocol": entry.protocol, "startPort": entry.port, "endPort": entry.port, "srcNetwork": []string{state.TierID}, "dstNetwork": []string{state.ExternalTierID}, "srcAddress": []string{state.PrivateIP + "/32"}, "srcNat": true}); e != nil {
			return e
		}
	}
	return save("BOOTSTRAP_ACCESS_POLICIES_CONFIRMED")
}
func connectSSH(ctx context.Context) (*ssh.Client, error) {
	if state.PublicIP == "" || state.Deleted {
		return nil, errors.New("fixture public endpoint unavailable")
	}
	key, e := os.ReadFile(keyPath)
	if e != nil {
		return nil, errors.New("bootstrap private key unavailable")
	}
	signer, e := ssh.ParsePrivateKey(key)
	if e != nil {
		return nil, errors.New("bootstrap private key invalid")
	}
	pins := filepath.Join(filepath.Dir(keyPath), "lbt145-bootstrap-known_hosts")
	f, e := os.OpenFile(pins, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return nil, errors.New("bootstrap host pins unavailable")
	}
	f.Close()
	verify, e := knownhosts.New(pins)
	if e != nil {
		return nil, errors.New("bootstrap host pins invalid")
	}
	alias := net.JoinHostPort(state.ServerID+".lbt145-bootstrap", "22")
	callback := func(_ string, remote net.Addr, key ssh.PublicKey) error {
		e := verify(alias, remote, key)
		var unknown *knownhosts.KeyError
		if errors.As(e, &unknown) && len(unknown.Want) == 0 {
			f, e := os.OpenFile(pins, os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				return errors.New("host pin save unavailable")
			}
			_, e = f.WriteString(knownhosts.Line([]string{alias}, key) + "\n")
			f.Close()
			return e
		}
		return e
	}
	connection, e := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(state.PublicIP, "22"))
	if e != nil {
		return nil, errors.New("bootstrap TCP22 unavailable")
	}
	closeOnCancellation(ctx, connection)
	connection.SetDeadline(time.Now().Add(30 * time.Second))
	username := os.Getenv("LBT145_BOOTSTRAP_SSH_USER")
	if username == "" {
		username = "labbit"
	}
	client, channels, requests, e := ssh.NewClientConn(connection, alias, &ssh.ClientConfig{User: username, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: callback})
	if e != nil {
		connection.Close()
		if strings.Contains(e.Error(), "unable to authenticate") {
			fmt.Println("SSH safe failure: public-key authentication rejected for user", username)
		}
		if strings.Contains(e.Error(), "knownhosts") {
			fmt.Println("SSH safe failure: host-key verification rejected")
		}
		return nil, errors.New("bootstrap authenticated SSH unavailable")
	}
	connection.SetDeadline(time.Time{})
	return ssh.NewClient(client, channels, requests), nil
}
func checkSSH(ctx context.Context) error {
	client, e := connectSSH(ctx)
	if e != nil {
		return e
	}
	defer client.Close()
	session, e := client.NewSession()
	if e != nil {
		return errors.New("SSH session unavailable")
	}
	defer session.Close()
	out, e := session.Output("uname -s; uname -m; id -un; cloud-init status --wait")
	if e != nil {
		return errors.New("bootstrap cloud-init SSH check failed")
	}
	fmt.Println("Connector host:", strings.TrimSpace(string(out)))
	return save("BOOTSTRAP_AUTHENTICATED_SSH_PASS")
}
func cleanupAccess(ctx context.Context) error {
	if len(state.PolicyAttempted) > 0 {
		for _, job := range state.PolicyJobs {
			if e := waitJob(ctx, job); e != nil {
				return e
			}
		}
		pp, e := nsmList[policy](ctx, "firewall/policy")
		if e != nil {
			return e
		}
		for _, p := range pp {
			if strings.HasPrefix(p.Comment, state.Name+":bootstrap-") {
				found := false
				for _, id := range state.PolicyIDs {
					found = found || id == p.ID
				}
				if !found {
					return errors.New("untracked bootstrap policy candidate; manual ownership review required")
				}
			}
		}
		if e = save("ACCESS_CLEANUP_IDS_CONFIRMED"); e != nil {
			return e
		}
	}
	for _, entry := range []struct {
		path string
		ids  []string
	}{{"firewall/policy", state.PolicyIDs}, {"portforwarding", []string{state.ForwardID}}, {"publicIp", []string{state.PublicID}}} {
		for _, id := range entry.ids {
			if id == "" {
				continue
			}
			present := false
			switch entry.path {
			case "firewall/policy":
				items, e := nsmList[policy](ctx, entry.path)
				if e != nil {
					return e
				}
				for _, item := range items {
					present = present || item.ID == id
				}
			case "portforwarding":
				items, e := nsmList[forwarding](ctx, entry.path)
				if e != nil {
					return e
				}
				for _, item := range items {
					present = present || item.ID == id
				}
			case "publicIp":
				items, e := nsmList[publicIP](ctx, entry.path)
				if e != nil {
					return e
				}
				for _, item := range items {
					present = present || item.ID == id
				}
			}
			if !present {
				continue
			}
			_, e := nsm.Delete(ctx, nsm.ServiceURL(entry.path, id), &gophercloud.RequestOpts{OkCodes: []int{200, 202, 204}})
			if e != nil && !gophercloud.ResponseCodeIs(e, 404) {
				return fmt.Errorf("bootstrap access delete: %s", safe(e))
			}
			for {
				found := false
				switch entry.path {
				case "firewall/policy":
					items, e := nsmList[policy](ctx, entry.path)
					if e != nil {
						return e
					}
					for _, item := range items {
						found = found || item.ID == id
					}
				case "portforwarding":
					items, e := nsmList[forwarding](ctx, entry.path)
					if e != nil {
						return e
					}
					for _, item := range items {
						found = found || item.ID == id
					}
				case "publicIp":
					items, e := nsmList[publicIP](ctx, entry.path)
					if e != nil {
						return e
					}
					for _, item := range items {
						found = found || item.ID == id
					}
				}
				if !found {
					break
				}
				select {
				case <-ctx.Done():
					return errors.New("access cleanup confirmation timeout")
				case <-time.After(2 * time.Second):
				}
			}
		}
	}
	return nil
}
