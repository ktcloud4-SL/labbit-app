// Explicit LBT-145 bootstrap fixture. Secrets never enter the resource ledger.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/config/clouds"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	openstackprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"golang.org/x/crypto/ssh"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const base = "https://api.ucloudbiz.olleh.com/d1/"

type tier struct {
	ID   string `json:"networkId"`
	Ref  string `json:"refId"`
	Name string `json:"networkName"`
	CIDR string `json:"cidr"`
}
type ledger struct {
	Name             string            `json:"name"`
	Stage            string            `json:"stage"`
	Project          string            `json:"projectId"`
	Image            string            `json:"imageId"`
	ImageName        string            `json:"imageName"`
	Flavor           string            `json:"flavorId"`
	FlavorName       string            `json:"flavorName"`
	CPU              int               `json:"vcpus"`
	RAM              int               `json:"ramMiB"`
	Disk             int               `json:"diskGiB"`
	TierID           string            `json:"tierId"`
	TierRef          string            `json:"tierRefId"`
	ExternalTierID   string            `json:"externalTierId"`
	KeyName          string            `json:"keypairName"`
	KeyCreated       bool              `json:"keyCreated"`
	ServerID         string            `json:"serverId"`
	VolumeIDs        []string          `json:"volumeIds"`
	PrivateIP        string            `json:"privateIP"`
	SourceCIDR       string            `json:"sourceCIDR"`
	PublicID         string            `json:"publicIpId"`
	PublicIP         string            `json:"publicIP"`
	ForwardID        string            `json:"forwardId"`
	PolicyIDs        []string          `json:"policyIds"`
	JobIDs           []string          `json:"jobIds"`
	Deleted          bool              `json:"deleted"`
	PolicyJobs       map[string]string `json:"policyJobs,omitempty"`
	PolicyAttempted  map[string]bool   `json:"policyAttempted,omitempty"`
	PublicAttempted  bool              `json:"publicIpCreateAttempted,omitempty"`
	ForwardAttempted bool              `json:"forwardCreateAttempted,omitempty"`
}

var state ledger
var ledgerPath, keyPath string
var compute, image, volume, nsm *gophercloud.ServiceClient

type traceTransport struct{ inner http.RoundTripper }

func (t traceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, e := t.inner.RoundTrip(r)
	if response != nil && r.Method == "POST" {
		fmt.Printf("Observed API mutation: method=POST route=%s status=%d\n", strings.TrimPrefix(r.URL.Path, "/d1/"), response.StatusCode)
		if r.URL.Path == "/d1/server/servers" || response.StatusCode >= 400 {
			b, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			response.Body = io.NopCloser(bytes.NewReader(b))
			if readErr == nil {
				var envelope map[string]json.RawMessage
				if json.Unmarshal(b, &envelope) == nil {
					keys := make([]string, 0, len(envelope))
					for k := range envelope {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					fmt.Println("Mutation response envelope keys:", keys)
					for _, k := range []string{"httpStatus", "code", "statusCode"} {
						var code int
						if json.Unmarshal(envelope[k], &code) == nil {
							fmt.Printf("Mutation body %s: %d\n", k, code)
						}
					}
					for _, field := range []string{"computeFault", "badRequest", "error"} {
						var fault map[string]json.RawMessage
						if json.Unmarshal(envelope[field], &fault) == nil {
							fields := make([]string, 0, len(fault))
							for key := range fault {
								fields = append(fields, key)
							}
							sort.Strings(fields)
							fmt.Println("Fault field names:", fields)
							var message string
							if json.Unmarshal(fault["message"], &message) == nil {
								words := regexp.MustCompile(`[A-Za-z_]+`).FindAllString(strings.ToLower(message), -1)
								safeWords := make([]string, 0, len(words))
								vocabulary := " flavor flavorref flavor_id id identifier found could can cannot not be no missing does exist exists is are required parameter parameters property properties invalid valid type string number integer null none format image server request compute size disk ram cpu memory quota small too minimum maximum enough larger less than greater exceeds available allowed permitted supported disabled input value provided specified version detail unexpected field fields unknown recognized unrecognized used use with without one of only must should at least all an a the and or for to from in on by this that failed failure error unavailable unable cannot schema validation min max length name vcpus boot index volume destination source linux os windows lookup database parse parsing mapping bad internal exception message argument empty non valid provide includes include get select selected selection correct incorrect match found accepted acceptable api api_version length pattern enum different attached network operation create "
								for _, word := range words {
									if strings.Contains(vocabulary, " "+word+" ") {
										safeWords = append(safeWords, word)
									} else {
										safeWords = append(safeWords, "[omitted]")
									}
								}
								if len(safeWords) > 40 {
									safeWords = safeWords[:40]
								}
								fmt.Println("Fault vocabulary (all non-allowlisted tokens omitted):", strings.Join(safeWords, " "))
							}
							for _, key := range []string{"code", "status", "statusCode"} {
								var code int
								if json.Unmarshal(fault[key], &code) == nil {
									fmt.Printf("Fault %s: %d\n", key, code)
								}
							}
						}
					}
				}
				lower := strings.ToLower(string(b))
				for label, pattern := range map[string]string{"flavor_missing": `flavor.{0,160}(could not be found|not found|does not exist|not exist)`, "flavor_not_permitted": `flavor.{0,160}(not allowed|not permitted|unsupported)`, "capacity_too_small": `(too small|insufficient|minimum|small for)`, "json_shape_invalid": `(not of type|not a valid|invalid type|must be|is required)`, "no_host_available": `(no valid host|novalidhost|no host)`, "request_validation": `(schema|validation|validate)`, "flavor_unavailable": `(disabled|not available|inactive|no match)`} {
					if regexp.MustCompile(pattern).MatchString(lower) {
						fmt.Println("Safe error classification:", label)
					}
				}
				for _, category := range []string{"quota", "flavor", "network", "image", "keypair", "key_name", "required", "availability", "volume", "usage_plan", "unauthorized", "forbidden", "unsupported", "not found", "internal server error", "bad request", "invalid", "null", "json", "token", "validation", "connection", "timeout", "billing", "예약", "과금", "권한", "오류", "실패", "할당", "부족"} {
					if strings.Contains(lower, category) {
						fmt.Println("Error category keyword:", category)
					}
				}
			}
		}
	}
	return response, e
}

func safe(err error) string {
	if err == nil {
		return "ok"
	}
	var e gophercloud.ErrUnexpectedResponseCode
	if errors.As(err, &e) {
		return fmt.Sprintf("HTTP %d", e.Actual)
	}
	return "request/response unavailable; raw payload omitted"
}
func save(stage string) error {
	state.Stage = stage
	b, e := json.MarshalIndent(state, "", "  ")
	if e != nil {
		return errors.New("ledger encode failed")
	}
	if e = os.WriteFile(ledgerPath, append(b, '\n'), 0600); e != nil {
		return errors.New("ledger save failed")
	}
	fmt.Println("Bootstrap:", stage)
	return nil
}
func setup(ctx context.Context) error {
	ledgerPath = os.Getenv("LBT145_BOOTSTRAP_LEDGER")
	keyPath = os.Getenv("LBT145_BOOTSTRAP_KEY")
	if !filepath.IsAbs(ledgerPath) || !filepath.IsAbs(keyPath) || os.Getenv("LABBIT_KTCLOUD_D1_BOOTSTRAP") != "1" {
		return errors.New("explicit gate and absolute local paths are required")
	}
	if b, e := os.ReadFile(ledgerPath); e == nil {
		if json.Unmarshal(b, &state) != nil {
			return errors.New("ledger decode failed")
		}
	}
	auth, _, tlsConfig, e := clouds.Parse(clouds.WithLocations(os.Getenv("LABBIT_PROVIDER_CONFIG_FILE")), clouds.WithCloudName(os.Getenv("OS_CLOUD")))
	if e != nil || strings.TrimRight(auth.IdentityEndpoint, "/") != strings.TrimRight(base+"identity/", "/") || tlsConfig != nil && tlsConfig.InsecureSkipVerify {
		return errors.New("verified explicit KT D1 cloud is required")
	}
	p, e := openstack.NewClient(base + "identity/")
	if e != nil {
		return errors.New("client unavailable")
	}
	p.HTTPClient.Timeout = 30 * time.Second
	p.HTTPClient.Transport = traceTransport{http.DefaultTransport}
	id := &gophercloud.ServiceClient{ProviderClient: p, Endpoint: base + "identity/"}
	result := tokens.Create(ctx, id, &auth)
	if e = p.SetTokenAndAuthResult(result); e != nil {
		return errors.New("authentication failed")
	}
	project, e := result.ExtractProject()
	if e != nil || project.ID == "" {
		return errors.New("project scope unavailable")
	}
	if state.Project != "" && state.Project != project.ID {
		return errors.New("ledger project affinity mismatch")
	}
	state.Project = project.ID
	compute = &gophercloud.ServiceClient{ProviderClient: p, Endpoint: base + "server/"}
	image = &gophercloud.ServiceClient{ProviderClient: p, Endpoint: base + "image/"}
	volume = &gophercloud.ServiceClient{ProviderClient: p, Endpoint: base + "volume/" + project.ID + "/"}
	nsm = &gophercloud.ServiceClient{ProviderClient: p, Endpoint: base + "nsm/v1/"}
	return nil
}
func readTiers(ctx context.Context) ([]tier, error) {
	var r struct {
		HTTP       int `json:"httpStatus"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
		Data []tier `json:"data"`
	}
	_, e := nsm.Get(ctx, nsm.ServiceURL("network")+"?networkType=ALL&size=2000&page=1", &r, nil)
	if e != nil {
		return nil, e
	}
	if r.HTTP != 200 || r.Data == nil || r.Pagination.Total != len(r.Data) {
		return nil, errors.New("Tier inventory is incomplete")
	}
	return r.Data, nil
}
func inventory(ctx context.Context) error {
	if state.Name == "" {
		r := make([]byte, 6)
		if _, e := rand.Read(r); e != nil {
			return e
		}
		state.Name = "labbit-lbt145-connector-" + hex.EncodeToString(r)
		state.KeyName = state.Name + "-key"
	}
	ipages, e := images.List(image, images.ListOpts{}).AllPages(ctx)
	if e != nil {
		return fmt.Errorf("image inventory: %s", safe(e))
	}
	ii, e := images.ExtractImages(ipages)
	if e != nil {
		return errors.New("image decode unavailable")
	}
	for _, im := range ii {
		fmt.Printf("Image option: %s status=%s\n", im.Name, im.Status)
		if strings.Contains(strings.ToLower(im.Name), "ubuntu") && string(im.Status) == "active" && (state.Image == "" || strings.Contains(im.Name, "22")) {
			state.Image = im.ID
			state.ImageName = im.Name
		}
	}
	selectedImage, e := images.Get(ctx, image, state.Image).Extract()
	if e != nil {
		return errors.New("selected image details unavailable")
	}
	fmt.Printf("Selected image constraints: minDisk=%d minRAM=%d\n", selectedImage.MinDiskGigabytes, selectedImage.MinRAMMegabytes)
	fpages, e := flavors.ListDetail(compute, flavors.ListOpts{}).AllPages(ctx)
	if e != nil {
		return fmt.Errorf("flavor inventory: %s", safe(e))
	}
	ff, e := flavors.ExtractFlavors(fpages)
	if e != nil {
		return errors.New("flavor decode unavailable")
	}
	sort.Slice(ff, func(i, j int) bool {
		if ff[i].RAM == ff[j].RAM {
			return ff[i].VCPUs < ff[j].VCPUs
		}
		return ff[i].RAM < ff[j].RAM
	})
	for _, f := range ff {
		if f.VCPUs >= 1 && f.RAM >= 2048 && f.RAM <= 4096 && !strings.Contains(strings.ToLower(f.Name), "win") {
			fmt.Printf("Small flavor option: %s CPU=%d RAM=%d disk=%d public=%t\n", f.Name, f.VCPUs, f.RAM, f.Disk, f.IsPublic)
			if (state.Flavor == "" && os.Getenv("LBT145_BOOTSTRAP_FLAVOR") == "") || f.Name == os.Getenv("LBT145_BOOTSTRAP_FLAVOR") {
				state.Flavor = f.ID
				state.FlavorName = f.Name
				state.CPU = f.VCPUs
				state.RAM = f.RAM
				state.Disk = f.Disk
			}
		}
	}
	tt, e := readTiers(ctx)
	if e != nil {
		return e
	}
	for _, t := range tt {
		if t.Name == "DMZ" && t.CIDR == "172.25.0.0/24" && t.Ref != "" {
			state.TierID = t.ID
			state.TierRef = t.Ref
		}
		if t.Ref == "" && t.CIDR == "" {
			state.ExternalTierID = t.ID
		}
	}
	if state.Image == "" || state.Flavor == "" || state.TierRef == "" {
		return errors.New("unambiguous bootstrap choices unavailable")
	}
	selectedFlavor, e := flavors.Get(ctx, compute, state.Flavor).Extract()
	if e != nil || selectedFlavor == nil || selectedFlavor.ID != state.Flavor {
		return errors.New("selected flavor detail/identity could not be confirmed")
	}
	fmt.Println("Selected flavor detail ID/name confirmed:", selectedFlavor.Name)
	fmt.Printf("Selected Connector fixture: image=%s flavor=%s CPU=%d RAM=%d disk=%d; DMZ UUID/refId separated\n", state.ImageName, state.FlavorName, state.CPU, state.RAM, state.Disk)
	return save("READ_ONLY_CHOICES_CONFIRMED")
}
func getServer(ctx context.Context, id string) (*servers.Server, error) {
	r := servers.Get(ctx, compute, id)
	item, e := r.Extract()
	if e == nil && item != nil && item.ID != "" {
		if item.ID != id || item.TenantID != state.Project {
			return nil, errors.New("server exact identity/project mismatch")
		}
		return item, nil
	}
	if r.Err != nil {
		return nil, r.Err
	}
	var response struct {
		Server servers.Server `json:"servers"`
	}
	if e = r.ExtractIntoStructPtr(&response.Server, "servers"); e != nil || response.Server.ID != id || response.Server.TenantID != state.Project {
		return nil, errors.New("server detail shape unavailable")
	}
	fmt.Println("Observed KT server detail uses plural servers envelope")
	return &response.Server, nil
}
func recordServer(ctx context.Context, s *servers.Server) error {
	if s == nil || s.ID != state.ServerID {
		return errors.New("bootstrap server mapping unconfirmed")
	}
	if s.Status == "ACTIVE" {
		if len(s.AttachedVolumes) != 1 || s.AttachedVolumes[0].ID == "" {
			return errors.New("bootstrap root mapping ambiguous")
		}
		root, e := volumes.Get(ctx, volume, s.AttachedVolumes[0].ID).Extract()
		if e != nil || root == nil || root.ID != s.AttachedVolumes[0].ID || root.Size != 50 || root.VolumeImageMetadata["image_id"] != state.Image || len(root.Attachments) != 1 || root.Attachments[0].ServerID != state.ServerID {
			return errors.New("bootstrap root mapping unconfirmed")
		}
		if len(state.VolumeIDs) > 1 || (len(state.VolumeIDs) == 1 && state.VolumeIDs[0] != root.ID) {
			return errors.New("bootstrap known root identity changed")
		}
		state.VolumeIDs = []string{root.ID}
	}
	for _, value := range s.Addresses {
		b, _ := json.Marshal(value)
		var aa []struct {
			Address string `json:"addr"`
			Type    string `json:"OS-EXT-IPS:type"`
		}
		if json.Unmarshal(b, &aa) != nil {
			continue
		}
		for _, a := range aa {
			if a.Type == "fixed" && strings.HasPrefix(a.Address, "172.25.0.") {
				state.PrivateIP = a.Address
			}
		}
	}
	return save("SERVER_" + s.Status + "_OBSERVED")
}
func create(ctx context.Context) error {
	if state.Name == "" || state.Image == "" || state.TierRef == "" {
		return errors.New("inventory phase is required")
	}
	if state.ServerID != "" || state.KeyCreated || state.Deleted {
		return errors.New("create was already attempted; inspect/reconcile instead")
	}
	pages, e := servers.List(compute, servers.ListOpts{}).AllPages(ctx)
	if e != nil {
		return errors.New("baseline server inventory unavailable")
	}
	items, e := servers.ExtractServers(pages)
	if e != nil || len(items) != 0 {
		return errors.New("bootstrap requires the verified empty server baseline")
	}
	_, e = os.Stat(keyPath)
	if e == nil {
		return errors.New("bootstrap key path already exists")
	}
	_, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return errors.New("key generation failed")
	}
	der, e := x509.MarshalPKCS8PrivateKey(private)
	if e != nil {
		return errors.New("key encoding failed")
	}
	if e = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); e != nil {
		return errors.New("private key save failed")
	}
	signer, e := ssh.NewSignerFromKey(private)
	if e != nil {
		return errors.New("public key unavailable")
	}
	if e = save("KEY_IMPORT_ATTEMPTED"); e != nil {
		return e
	}
	key, e := keypairs.Create(ctx, compute, keypairs.CreateOpts{Name: state.KeyName, PublicKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))}).Extract()
	if e != nil || key.Name != state.KeyName {
		return fmt.Errorf("key import: %s", safe(e))
	}
	state.KeyCreated = true
	if e = save("KEY_IMPORT_CONFIRMED"); e != nil {
		return e
	}
	if e = verifyRegisteredKey(ctx); e != nil {
		return e
	}
	if e = save("SERVER_CREATE_ATTEMPTED"); e != nil {
		return e
	}
	userData := "#cloud-config\nusers:\n  - default\n  - name: labbit\n    groups: [sudo]\n    shell: /bin/bash\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    ssh_authorized_keys:\n      - " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + "\nssh_pwauth: false\n"
	opts := keypairs.CreateOptsExt{CreateOptsBuilder: servers.CreateOpts{Name: state.Name, FlavorRef: state.Flavor, AvailabilityZone: "DX-M1", UserData: []byte(userData), Networks: []servers.Network{{UUID: state.TierRef}}, BlockDevice: []servers.BlockDevice{{SourceType: servers.SourceImage, UUID: state.Image, DestinationType: servers.DestinationVolume, BootIndex: 0, VolumeSize: 50, DeleteOnTermination: true}}, Metadata: map[string]string{"labbit_test": "LBT-145", "labbit_fixture": state.Name}}, KeyName: state.KeyName}
	body, e := opts.ToServerCreateMap()
	if e != nil {
		return errors.New("server request mapping failed")
	}
	if os.Getenv("LBT145_BOOTSTRAP_STRICT_DOCUMENTED") == "1" {
		serverMap := body["server"].(map[string]any)
		delete(serverMap, "imageRef")
		devices := serverMap["block_device_mapping_v2"].([]any)
		for _, device := range devices {
			device.(map[string]any)["boot_index"] = "0"
		}
		fmt.Println("Using documented boot-from-image volume mapping without empty imageRef")
	}
	var result servers.CreateResult
	response, e := compute.Post(ctx, compute.ServiceURL("servers"), body, &result.Body, &gophercloud.RequestOpts{OkCodes: []int{200, 202}})
	_, result.Header, result.Err = gophercloud.ParseResponse(response, e)
	server, e := result.Extract()
	if server != nil {
		state.ServerID = server.ID
	}
	if err := save("SERVER_CREATE_RESPONSE_RECORDED"); err != nil {
		return err
	}
	if e != nil || state.ServerID == "" {
		return fmt.Errorf("server create uncertain; reconcile before any retry: %s", safe(e))
	}
	for {
		item, e := getServer(ctx, state.ServerID)
		if e != nil {
			return fmt.Errorf("server detail: %s", safe(e))
		}
		if e = recordServer(ctx, item); e != nil {
			return e
		}
		if item.Status == "ACTIVE" {
			fmt.Println("Connector VM ACTIVE; physical Tier refId accepted; root volume count:", len(state.VolumeIDs))
			return nil
		}
		if item.Status == "ERROR" {
			return errors.New("server entered ERROR; raw fault omitted")
		}
		select {
		case <-ctx.Done():
			return errors.New("server wait timeout")
		case <-time.After(5 * time.Second):
		}
	}
}
func inspect(ctx context.Context) error {
	pages, e := servers.List(compute, servers.ListOpts{}).AllPages(ctx)
	if e != nil {
		return fmt.Errorf("server inventory: %s", safe(e))
	}
	items, e := servers.ExtractServers(pages)
	if e != nil {
		return errors.New("server inventory decode unavailable")
	}
	fmt.Println("Project server count:", len(items))
	for _, s := range items {
		if state.ServerID == "" && (s.Name == state.Name || s.Metadata["labbit_fixture"] == state.Name) {
			return errors.New("unowned server candidate observed; never adopt or delete by name")
		}
		if state.ServerID != "" && s.ID == state.ServerID {
			detail, e := getServer(ctx, s.ID)
			if e != nil {
				return fmt.Errorf("server detail: %s", safe(e))
			}
			if detail.Name != state.Name || detail.KeyName != state.KeyName || detail.Metadata["labbit_fixture"] != state.Name || detail.Metadata["labbit_test"] != "LBT-145" {
				return errors.New("known diagnostic server ownership/name/key mismatch; no deletion allowed")
			}
			if e = recordServer(ctx, detail); e != nil {
				return e
			}
			fmt.Println("Tracked Connector VM status:", detail.Status)
		}
	}
	vpages, e := volumes.List(volume, volumes.ListOpts{}).AllPages(ctx)
	if e != nil {
		return fmt.Errorf("volume inventory: %s", safe(e))
	}
	vi, e := volumes.ExtractVolumes(vpages)
	if e != nil {
		return errors.New("volume inventory decode unavailable")
	}
	fmt.Println("Project volume count:", len(vi))
	return save("READ_ONLY_INSPECTION_COMPLETE")
}
func cleanup(ctx context.Context) error {
	if e := inspect(ctx); e != nil {
		return e
	}
	if e := cleanupAccess(ctx); e != nil {
		return e
	}
	if state.ServerID != "" {
		cfg := openstackprovider.ConfigFromEnvironment()
		cfg.Provision.ProjectID = state.Project
		cfg.Provision.KTCloudConnectorTierID = state.TierID
		adapter, e := openstackprovider.New(ctx, cfg)
		if e != nil {
			return errors.New("bootstrap cleanup Provider unavailable")
		}
		owned := []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeServer, ProviderID: state.ServerID, Generation: 1, LogicalName: "bootstrap"}}
		for _, id := range state.VolumeIDs {
			owned = append(owned, coreprovider.ResourceRef{ResourceType: coreprovider.ResourceTypeVolume, ProviderID: id, Generation: 1, LogicalName: "bootstrap-root"})
		}
		if e = save("BOOTSTRAP_COMPUTE_CLEANUP_ATTEMPTED"); e != nil {
			return e
		}
		result, e := adapter.Cleanup(ctx, coreprovider.CleanupRequest{Correlation: coreprovider.Correlation{OperationID: state.Name + "-cleanup", LabInstanceID: state.Name, Generation: 1}, ProviderResources: owned})
		if e != nil || result.Outcome != coreprovider.OutcomeSucceeded {
			fmt.Println("Bootstrap safe Cleanup outcome:", result.Outcome)
			if result.Error != nil {
				fmt.Println("Bootstrap safe Cleanup error:", result.Error.Code, result.Error.Message)
			}
			for _, r := range result.ProviderResources {
				fmt.Println("Bootstrap Cleanup resource state:", r.ResourceType, r.ObservedState)
			}
			return errors.New("bootstrap compute cleanup not confirmed; reconcile exact tracked IDs")
		}
		if actual := servers.Get(ctx, compute, state.ServerID); !gophercloud.ResponseCodeIs(actual.Err, 404) {
			return errors.New("bootstrap actual server 404 required")
		}
		for _, id := range state.VolumeIDs {
			if actual := volumes.Get(ctx, volume, id); !gophercloud.ResponseCodeIs(actual.Err, 404) {
				return errors.New("bootstrap actual root volume 404 required")
			}
		}
	}
	if state.KeyCreated {
		if e := keypairs.Delete(ctx, compute, state.KeyName, nil).ExtractErr(); e != nil && !gophercloud.ResponseCodeIs(e, 404) {
			return fmt.Errorf("key delete: %s", safe(e))
		}
		if _, e := keypairs.Get(ctx, compute, state.KeyName, nil).Extract(); !gophercloud.ResponseCodeIs(e, 404) {
			return errors.New("key absence not confirmed")
		}
	}
	state.Deleted = true
	return save("BOOTSTRAP_CLEANUP_CONFIRMED")
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
	defer cancel()
	phase := os.Getenv("LBT145_BOOTSTRAP_PHASE")
	allowed := map[string]bool{"inventory": true, "create": true, "inspect": true, "access": true, "ssh": true, "observe-ssh": true, "deploy": true, "control": true, "recover-observe": true, "recover-cleanup": true, "archive-control": true, "collect": true, "residual": true, "cleanup": true, "final": true}
	if os.Getenv("LBT145_PREMERGE_LIVE_TEST") != "1" || os.Getenv("LBT145_CONTROL_GENERATIONS") != "2" || !allowed[phase] {
		fmt.Println("Explicit two-generation premerge fixture gate required")
		os.Exit(1)
	}
	e := setup(ctx)
	if e == nil {
		switch phase {
		case "inventory":
			if _, err := os.Stat(ledgerPath); !os.IsNotExist(err) {
				e = errors.New("fresh fixture required")
				break
			}
			e = inventory(ctx)
			if e == nil {
				var baseline inventories
				baseline, e = inventoryIDs(ctx)
				if e == nil && (len(baseline["SERVER"]) != 0 || len(baseline["VOLUME"]) != 0) {
					e = errors.New("nonempty compute baseline prevents fixture creation")
				}
				if e == nil {
					e = writeJSON("baseline-inventory.json", baseline)
				}
			}
		case "create":
			e = create(ctx)
		case "inspect":
			e = inspect(ctx)
		case "access":
			e = access(ctx)
		case "ssh":
			e = checkSSH(ctx)
		case "observe-ssh":
			e = observeLiveSSH(ctx)
		case "deploy":
			e = deployControl(ctx)
		case "control":
			e = runControl(ctx)
		case "recover-observe", "recover-cleanup":
			e = runRecovery(ctx, phase == "recover-observe")
		case "archive-control":
			e = archiveControl(ctx)
		case "collect":
			e = collectControl(ctx)
		case "residual":
			e = recordOwnedResidual(ctx)
		case "cleanup":
			e = cleanup(ctx)
		case "final":
			e = final(ctx)
		}
	}
	if e != nil {
		fmt.Println("Premerge bootstrap failed:", safe(e))
		fmt.Println("Safe stage:", state.Stage)
		os.Exit(1)
	}
}
