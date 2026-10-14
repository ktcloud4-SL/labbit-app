package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func writeJSON(name string, data any) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(ledgerPath), name), append(b, '\n'), 0600)
}

type inventories map[string][]string

func inventoryIDs(ctx context.Context) (inventories, error) {
	result := inventories{}
	pages, e := servers.List(compute, servers.ListOpts{}).AllPages(ctx)
	if e != nil {
		return nil, e
	}
	ss, e := servers.ExtractServers(pages)
	if e != nil {
		return nil, e
	}
	for _, s := range ss {
		result["SERVER"] = append(result["SERVER"], s.ID)
	}
	vp, e := volumes.List(volume, volumes.ListOpts{}).AllPages(ctx)
	if e != nil {
		return nil, e
	}
	vs, e := volumes.ExtractVolumes(vp)
	if e != nil {
		return nil, e
	}
	for _, v := range vs {
		result["VOLUME"] = append(result["VOLUME"], v.ID)
	}
	kp, e := keypairs.List(compute, nil).AllPages(ctx)
	if e != nil {
		return nil, e
	}
	ks, e := keypairs.ExtractKeyPairs(kp)
	if e != nil {
		return nil, e
	}
	for _, k := range ks {
		result["KEYPAIR"] = append(result["KEYPAIR"], k.Name)
	}
	tiers, e := readTiers(ctx)
	if e != nil {
		return nil, e
	}
	for _, t := range tiers {
		result["TIER"] = append(result["TIER"], t.ID)
	}
	ps, e := nsmList[policy](ctx, "firewall/policy")
	if e != nil {
		return nil, e
	}
	for _, p := range ps {
		result["POLICY"] = append(result["POLICY"], p.ID)
	}
	ips, e := nsmList[publicIP](ctx, "publicIp")
	if e != nil {
		return nil, e
	}
	for _, p := range ips {
		result["PUBLIC_IP"] = append(result["PUBLIC_IP"], p.ID)
	}
	fs, e := nsmList[forwarding](ctx, "portforwarding")
	if e != nil {
		return nil, e
	}
	for _, f := range fs {
		result["FORWARD"] = append(result["FORWARD"], f.ID)
	}
	for _, kind := range []string{"SERVER", "VOLUME", "KEYPAIR", "TIER", "POLICY", "PUBLIC_IP", "FORWARD"} {
		if result[kind] == nil {
			result[kind] = []string{}
		}
		sort.Strings(result[kind])
	}
	return result, nil
}
func final(ctx context.Context) error {
	current, err := inventoryIDs(ctx)
	if err != nil {
		return errors.New("final full inventory unavailable")
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(ledgerPath), "baseline-inventory.json"))
	if err != nil {
		return err
	}
	var baseline inventories
	if json.Unmarshal(b, &baseline) != nil {
		return errors.New("baseline invalid")
	}
	if len(baseline) != 7 {
		return errors.New("baseline kind coverage incomplete")
	}
	for _, kind := range []string{"SERVER", "VOLUME", "KEYPAIR", "TIER", "POLICY", "PUBLIC_IP", "FORWARD"} {
		if _, ok := baseline[kind]; !ok {
			return errors.New("baseline kind missing")
		}
	}
	mismatch := []string{}
	for kind, ids := range baseline {
		if strings.Join(ids, "\x00") != strings.Join(current[kind], "\x00") {
			mismatch = append(mismatch, kind)
		}
	}
	err = writeJSON("final-inventory.json", map[string]any{"atUTC": time.Now().UTC().Format(time.RFC3339Nano), "baselinePreserved": len(mismatch) == 0, "differentKinds": mismatch, "inventory": current, "deleted": state.Deleted, "serverId": state.ServerID})
	if err != nil {
		return err
	}
	if len(mismatch) > 0 || !state.Deleted {
		return errors.New("final baseline not restored or cleanup not confirmed")
	}
	fmt.Println("Final inventory: exactly original seven-kind baseline restored; all diagnostic cloud resources absent")
	return nil
}
