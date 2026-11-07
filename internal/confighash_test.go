package internal

import (
	"reflect"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestServiceHashIgnoresReplicas(t *testing.T) {
	one := 1
	two := 2
	hash1, err := serviceHash(types.ServiceConfig{Name: "foo", Image: "bar", Scale: &one, Deploy: &types.DeployConfig{Replicas: &one}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hash2, err := serviceHash(types.ServiceConfig{Name: "foo", Image: "bar", Scale: &two, Deploy: &types.DeployConfig{Replicas: &two}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("expected replica count to be excluded from hash, got %q and %q", hash1, hash2)
	}
}

// TestServiceHashGoldenValue uses the golden value from docker/compose v5.7.0
// internal/confighash/hash_test.go to verify the hash matches what Compose
// stamps on containers
func TestServiceHashGoldenValue(t *testing.T) {
	replicas := 3
	a := "1"
	var svc types.ServiceConfig
	svc.Name = "web"
	svc.Image = "nginx:latest"
	svc.Command = types.ShellCommand{"nginx", "-g", "daemon off;"}
	svc.User = "nobody"
	svc.Tty = true
	svc.StdinOpen = true
	svc.Restart = types.RestartPolicyAlways
	svc.Environment = types.MappingWithEquals{"A": &a, "B": nil}
	svc.Labels = types.Labels{"com.example": "v"}
	svc.Annotations = types.Mapping{"note": "x"}
	svc.CapAdd = []string{"NET_ADMIN"}
	svc.ExtraHosts = types.HostsList{"alpha": []string{"10.0.0.1"}}
	svc.Deploy = &types.DeployConfig{Replicas: &replicas}
	svc.Ports = []types.ServicePortConfig{
		{Target: 80, Published: "8080", Protocol: "tcp"},
	}
	svc.HealthCheck = &types.HealthCheckConfig{
		Test: types.HealthCheckTest{"CMD", "true"},
	}
	svc.Volumes = []types.ServiceVolumeConfig{
		{Type: types.VolumeTypeVolume, Source: "data", Target: "/data"},
	}
	svc.Networks = map[string]*types.ServiceNetworkConfig{"default": nil}

	hash, err := serviceHash(svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "75bc312132c71fac4971d123202631b6978ac9d796336621af744d46611b42a2"
	if hash != expected {
		t.Errorf("expected hash %q, got %q", expected, hash)
	}
}

// TestServiceHashKeyOrderCoversStruct fails when a compose-go upgrade adds a
// service attribute, signaling that serviceHashKeyOrder should be resynced
// with docker/compose
func TestServiceHashKeyOrderCoversStruct(t *testing.T) {
	known := map[string]bool{}
	for _, k := range serviceHashKeyOrder {
		known[k] = true
	}
	var walk func(rt reflect.Type)
	walk = func(rt reflect.Type) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if f.Anonymous && name == "" {
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if !known[name] {
				t.Errorf("ServiceConfig attribute %q is not in serviceHashKeyOrder, resync with docker/compose", name)
			}
		}
	}
	walk(reflect.TypeOf(types.ServiceConfig{}))
}
