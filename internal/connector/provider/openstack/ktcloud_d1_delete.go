package openstackprovider

import (
	"context"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/pagination"
)

type ktCloudVolumePage struct{ volumes.VolumePage }

func (p ktCloudVolumePage) IsEmpty() (bool, error) {
	body, ok := p.Body.(map[string]any)
	if !ok || p.StatusCode != 200 {
		return false, ErrReconcileLookup
	}
	if _, ok := body["volumes"].([]any); !ok {
		return false, ErrReconcileLookup
	}
	return p.VolumePage.IsEmpty()
}

func (a *Adapter) ktCloudRetainedRoot(ctx context.Context, serverID string) (bool, error) {
	items, err := a.listServerItems(ctx, servers.ListOpts{})
	if err != nil {
		return false, ErrReconcileLookup
	}
	for _, item := range items {
		if item.ID == serverID {
			return false, ErrReconcileLookup
		}
	}
	pager := volumes.List(a.volume, volumes.ListOpts{}).WithPageCreator(func(result pagination.PageResult) pagination.Page {
		return ktCloudVolumePage{volumes.VolumePage{LinkedPageBase: pagination.LinkedPageBase{PageResult: result}}}
	})
	retained := false
	err = pager.EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
		items, err := volumes.ExtractVolumes(page.(ktCloudVolumePage).VolumePage)
		if err != nil {
			return false, ErrReconcileLookup
		}
		for _, item := range items {
			if item.ID == "" {
				return false, ErrReconcileLookup
			}
			for _, attachment := range item.Attachments {
				if attachment.ServerID == serverID {
					retained = true
				}
			}
		}
		return true, nil
	})
	return retained, err
}

// KT's ordinary DELETE soft-deletes a VM and retains its attached boot volume.
// The documented forceDelete action is issued once, only after observing the
// exact owned VM absent from the complete inventory with a retained attachment.
// A failed/uncertain receipt is returned for Reconciliation, never retried here.
func (a *Adapter) deleteKTCloudServer(ctx context.Context, serverID string) error {
	_, err := a.lookupServer(ctx, serverID)
	if gophercloud.ResponseCodeIs(err, 404) {
		return nil
	}
	if err == nil {
		if err = servers.Delete(ctx, a.compute, serverID).ExtractErr(); err != nil {
			return err
		}
	} else if !gophercloud.ResponseCodeIs(err, 400) {
		return ErrResourceDelete
	}
	wait, cancel := context.WithTimeout(ctx, a.provision.ActiveTimeout)
	defer cancel()
	forced := false
	for {
		_, err = a.lookupServer(wait, serverID)
		if gophercloud.ResponseCodeIs(err, 404) {
			return nil
		}
		if gophercloud.ResponseCodeIs(err, 400) && !forced {
			retained, err := a.ktCloudRetainedRoot(wait, serverID)
			if err != nil {
				return ErrReconcileLookup
			}
			if !retained {
				return ErrReconcileLookup
			}
			forced = true
			if err = servers.ForceDelete(wait, a.compute, serverID).ExtractErr(); err != nil {
				return err
			}
		} else if err != nil && !gophercloud.ResponseCodeIs(err, 400) {
			return ErrReconcileLookup
		}
		select {
		case <-wait.Done():
			return ErrDeleteWait
		case <-time.After(a.provision.PollInterval):
		}
	}
}
