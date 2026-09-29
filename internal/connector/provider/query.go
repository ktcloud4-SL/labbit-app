package provider

import "context"

// QueryProvider exposes the read-only Provider operations used by
// PROVIDER_REQUEST without coupling Control to an OpenStack SDK.
type QueryProvider interface {
	ProviderConnectionID() string
	ValidateConnection(context.Context) error
	ListImages(context.Context) ([]Image, error)
	ListFlavors(context.Context) ([]Flavor, error)
}

type Image struct {
	ID     string
	Name   string
	Status string
}

type Flavor struct {
	ID      string
	Name    string
	VCPUs   int64
	RAMMiB  int64
	DiskGiB int64
}

type Server struct {
	ID     string
	Name   string
	Status string
}

type Network struct {
	ID        string
	Name      string
	Status    string
	Shared    bool
	SubnetIDs []string
}

type Subnet struct {
	ID         string
	Name       string
	NetworkID  string
	CIDR       string
	GatewayIP  string
	EnableDHCP bool
}

type SecurityGroup struct {
	ID          string
	Name        string
	Description string
}

const (
	ResourceTypeNetwork       = "NETWORK"
	ResourceTypeSubnet        = "SUBNET"
	ResourceTypeSecurityGroup = "SECURITY_GROUP"
	ResourceTypeSecurityRule  = "SECURITY_GROUP_RULE"
	ResourceTypeRouter        = "ROUTER"
	ResourceTypePort          = "PORT"
	ResourceTypeServer        = "SERVER"
)
