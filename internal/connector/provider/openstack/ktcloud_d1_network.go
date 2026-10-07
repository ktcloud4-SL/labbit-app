package openstackprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/gophercloud/gophercloud/v2"
)

var errKTCloudResponse = errors.New("KT D1 API response is unavailable")

// NSM owns Tier UUIDs and firewall policies; refId is only the physical
// Neutron network attachment ID. Neither IDs nor resource kinds are aliases.
type ktCloudTier struct {
	ID     string `json:"networkId"`
	RefID  string `json:"refId"`
	Name   string `json:"networkName"`
	CIDR   string `json:"cidr"`
	Status string `json:"status"`
	Shared bool   `json:"shared"`
}

type ktCloudInterface struct {
	ID   string `json:"networkId"`
	Name string `json:"name"`
}

type ktCloudAddress struct {
	Name string `json:"name"`
}
type ktCloudService struct {
	Protocol string `json:"protocol"`
	Start    string `json:"startPort"`
	End      string `json:"endPort"`
}

type ktCloudFirewallPolicy struct {
	ID             string             `json:"policyId"`
	Action         string             `json:"action"`
	Status         string             `json:"status"`
	Comment        string             `json:"comment"`
	NAT            string             `json:"nat"`
	Source         []ktCloudInterface `json:"srcInterface"`
	Destination    []ktCloudInterface `json:"dstInterface"`
	SourceIPs      []ktCloudAddress   `json:"srcAddress"`
	DestinationIPs []ktCloudAddress   `json:"dstAddress"`
	Services       []ktCloudService   `json:"services"`
}

type ktCloudReceipt struct {
	Status ktCloudStatus `json:"httpStatus"`
	JobID  string        `json:"jobId"`
	Data   struct {
		ID string `json:"networkId"`
	} `json:"data"`
}

// The official API uses both JSON numbers and numeric strings for httpStatus.
type ktCloudStatus int

func (s *ktCloudStatus) UnmarshalJSON(raw []byte) error {
	value := strings.Trim(string(raw), "\"")
	n, err := strconv.Atoi(value)
	if err != nil {
		return errKTCloudResponse
	}
	*s = ktCloudStatus(n)
	return nil
}

type ktCloudPage[T any] struct {
	Status     ktCloudStatus `json:"httpStatus"`
	Pagination struct {
		Total  int `json:"total"`
		Offset int `json:"offset"`
	} `json:"pagination"`
	Data []T `json:"data"`
}

type ktCloudD1Network struct{ client *gophercloud.ServiceClient }

func ktCloudList[T any](ctx context.Context, client *gophercloud.ServiceClient, resource string, query url.Values) ([]T, error) {
	if client == nil {
		return nil, ErrClientUnavailable
	}
	if query == nil {
		query = make(url.Values)
	}
	query.Set("size", "2000")
	items := make([]T, 0)
	for page := 1; page <= 256; page++ {
		query.Set("page", strconv.Itoa(page))
		var response ktCloudPage[T]
		_, err := client.Get(ctx, client.ServiceURL(resource)+"?"+query.Encode(), &response, nil)
		if err != nil {
			return nil, safeContextError(ctx, errKTCloudResponse)
		}
		if response.Status != 200 || response.Data == nil || response.Pagination.Total < 0 || response.Pagination.Offset != len(items) {
			return nil, errKTCloudResponse
		}
		items = append(items, response.Data...)
		if len(items) == response.Pagination.Total {
			return items, nil
		}
		if len(items) > response.Pagination.Total || len(response.Data) == 0 {
			return nil, errKTCloudResponse
		}
	}
	return nil, errKTCloudResponse
}

func (n *ktCloudD1Network) tiers(ctx context.Context) ([]ktCloudTier, error) {
	items, err := ktCloudList[ktCloudTier](ctx, n.client, "network", url.Values{"networkType": {"ALL"}})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.ID == "" {
			return nil, errKTCloudResponse
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, errKTCloudResponse
		}
		seen[item.ID] = struct{}{}
	}
	return items, nil
}

func (n *ktCloudD1Network) tier(ctx context.Context, id string) (ktCloudTier, bool, error) {
	items, err := n.tiers(ctx)
	if err != nil {
		return ktCloudTier{}, false, err
	}
	var found ktCloudTier
	count := 0
	for _, item := range items {
		if item.ID == id {
			found = item
			count++
		}
	}
	if count > 1 {
		return ktCloudTier{}, false, errKTCloudResponse
	}
	return found, count == 1, nil
}

func (n *ktCloudD1Network) createTier(ctx context.Context, name, cidr string) (ktCloudReceipt, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 24 || prefix != prefix.Masked() || strings.TrimSpace(name) == "" {
		return ktCloudReceipt{}, ErrInvalidResourceSpec
	}
	address := prefix.Addr().As4()
	ip := func(last byte) string { value := address; value[3] = last; return netip.AddrFrom4(value).String() }
	payload := map[string]any{"name": name, "type": "tier", "isCustom": true, "detail": map[string]string{
		"cidr": cidr, "gatewayIp": ip(1), "startIp": ip(6), "endIp": ip(180), "lbStartIp": ip(181), "lbEndIp": ip(199), "bmStartIp": ip(201), "bmEndIp": ip(250), "iscsiStartIp": ip(251), "iscsiEndIp": ip(254),
	}}
	var result ktCloudReceipt
	_, err = n.client.Post(ctx, n.client.ServiceURL("network"), payload, &result, &gophercloud.RequestOpts{OkCodes: []int{200, 201, 202}})
	if err == nil {
		err = ktCloudReceiptError(result.Status)
	}
	if err != nil {
		return result, safeMutationError(ctx, err, ErrNetworkCreate)
	}
	if result.Data.ID == "" {
		return result, ErrNetworkCreate
	}
	return result, nil
}

func (n *ktCloudD1Network) deleteTier(ctx context.Context, id string) error {
	return n.delete(ctx, "network", id)
}

func (n *ktCloudD1Network) policies(ctx context.Context) ([]ktCloudFirewallPolicy, error) {
	items, err := ktCloudList[ktCloudFirewallPolicy](ctx, n.client, "firewall/policy", nil)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.ID == "" {
			return nil, errKTCloudResponse
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, errKTCloudResponse
		}
		seen[item.ID] = struct{}{}
	}
	return items, nil
}

type ktCloudFirewallSpec struct {
	Accept         bool     `json:"action"`
	Protocol       string   `json:"protocol"`
	Start          string   `json:"startPort,omitempty"`
	End            string   `json:"endPort,omitempty"`
	Source         []string `json:"srcNetwork"`
	Destination    []string `json:"dstNetwork"`
	SourceIPs      []string `json:"srcAddress,omitempty"`
	DestinationIPs []string `json:"dstAddress,omitempty"`
	Comment        string   `json:"comment"`
	SourceNAT      bool     `json:"srcNat"`
}

func (n *ktCloudD1Network) createPolicy(ctx context.Context, spec ktCloudFirewallSpec) (ktCloudReceipt, error) {
	if len(spec.Source) != 1 || len(spec.Destination) != 1 || spec.Source[0] == "" || spec.Destination[0] == "" || spec.Comment == "" {
		return ktCloudReceipt{}, ErrInvalidResourceSpec
	}
	var result ktCloudReceipt
	_, err := n.client.Post(ctx, n.client.ServiceURL("firewall", "policy"), spec, &result, &gophercloud.RequestOpts{OkCodes: []int{200, 201, 202}})
	if err == nil {
		err = ktCloudReceiptError(result.Status)
	}
	if err != nil {
		return result, safeMutationError(ctx, err, ErrSecurityRuleCreate)
	}
	if result.JobID == "" {
		return result, ErrSecurityRuleCreate
	}
	return result, nil
}

func (n *ktCloudD1Network) deletePolicy(ctx context.Context, id string) error {
	return n.delete(ctx, "firewall/policy", id)
}

func (n *ktCloudD1Network) delete(ctx context.Context, resource, id string) error {
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, "/?#") {
		return errors.Join(ErrLifecycleRequest, ErrMutationRejected)
	}
	var result ktCloudReceipt
	response, err := n.client.Delete(ctx, n.client.ServiceURL(resource, url.PathEscape(id)), &gophercloud.RequestOpts{OkCodes: []int{200, 202, 204}, JSONResponse: &result})
	if gophercloud.ResponseCodeIs(err, 404) {
		// NSM has returned 404 for IDs still present in its complete inventory.
		// Confirm absence from that inventory rather than accepting the receipt.
		if resource == "network" {
			_, exists, lookupErr := n.tier(ctx, id)
			if lookupErr != nil || exists {
				return ErrReconcileLookup
			}
		} else {
			items, lookupErr := n.policies(ctx)
			if lookupErr != nil {
				return ErrReconcileLookup
			}
			for _, item := range items {
				if item.ID == id {
					return ErrReconcileLookup
				}
			}
		}
		return nil
	}
	if err == nil && response.StatusCode != 204 {
		err = ktCloudReceiptError(result.Status)
	}
	if err != nil {
		return safeMutationError(ctx, err, ErrResourceDelete)
	}
	return nil
}

func (n *ktCloudD1Network) job(ctx context.Context, id string) (string, error) {
	var result struct {
		Status    ktCloudStatus   `json:"httpStatus"`
		JobID     string          `json:"jobId"`
		JobStatus string          `json:"jobStatus"`
		Data      json.RawMessage `json:"data"`
	}
	_, err := n.client.Get(ctx, n.client.ServiceURL("job", "status", url.PathEscape(id)), &result, nil)
	if err != nil {
		return "", safeContextError(ctx, errKTCloudResponse)
	}
	if result.Status != 200 || result.JobID != id || result.JobStatus == "" {
		return "", errKTCloudResponse
	}
	return strings.ToUpper(result.JobStatus), nil
}

func ktCloudReceiptError(status ktCloudStatus) error {
	if status == 200 || status == 201 || status == 202 {
		return nil
	}
	if status >= 400 && status <= 599 {
		return gophercloud.ErrUnexpectedResponseCode{Actual: int(status)}
	}
	return errKTCloudResponse
}
