package openstackprovider

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/pagination"
)

// KT can return a fault envelope with HTTP 200. Such a page is unavailable,
// never an empty inventory permitting a second Create or confirming absence.
type ktCloudServerPage struct{ servers.ServerPage }

func (p ktCloudServerPage) IsEmpty() (bool, error) {
	body, ok := p.Body.(map[string]any)
	if !ok || p.StatusCode != 200 {
		return false, ErrServerList
	}
	if _, ok := body["servers"].([]any); !ok {
		return false, ErrServerList
	}
	return p.ServerPage.IsEmpty()
}

func (a *Adapter) lookupServer(ctx context.Context, id string) (*servers.Server, error) {
	item, err := servers.Get(ctx, a.compute, id).Extract()
	if a.ktNetwork == nil {
		return item, err
	}
	if err != nil {
		return nil, err
	}
	if item == nil || item.ID != id || item.Status == "" {
		return nil, ErrServerGet
	}
	return item, nil
}

func ktCloudServerQuotaCredit(item servers.Server) (int, int, error) {
	// KT exposes Nova's extended flavor representation without flavor.id.
	// Credit observed capacity only; never substitute a mutable snapshot.
	if name, ok := item.Flavor["original_name"].(string); !ok || strings.TrimSpace(name) == "" {
		return 0, 0, ErrQuotaLookup
	}
	decode := func(key string) (int, error) {
		b, err := json.Marshal(item.Flavor[key])
		var value int
		if err != nil || json.Unmarshal(b, &value) != nil || value <= 0 {
			return 0, ErrQuotaLookup
		}
		return value, nil
	}
	cores, err := decode("vcpus")
	if err != nil {
		return 0, 0, err
	}
	ram, err := decode("ram")
	return cores, ram, err
}

func (a *Adapter) listServerItems(ctx context.Context, opts servers.ListOpts) ([]servers.Server, error) {
	pager := servers.List(a.compute, opts)
	if a.ktNetwork == nil {
		pages, err := pager.AllPages(ctx)
		if err != nil {
			return nil, err
		}
		return servers.ExtractServers(pages)
	}
	pager = pager.WithPageCreator(func(result pagination.PageResult) pagination.Page {
		return ktCloudServerPage{servers.ServerPage{LinkedPageBase: pagination.LinkedPageBase{PageResult: result}}}
	})
	items := make([]servers.Server, 0)
	err := pager.EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
		batch, err := servers.ExtractServers(page.(ktCloudServerPage).ServerPage)
		if err != nil {
			return false, ErrServerList
		}
		for _, item := range batch {
			if item.ID == "" {
				return false, ErrServerList
			}
		}
		items = append(items, batch...)
		return true, nil
	})
	return items, err
}
