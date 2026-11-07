package internal

// The service config hash below is copied from docker/compose v5.7.0
// internal/confighash/hash.go (Apache License 2.0, Copyright 2020 Docker
// Compose CLI authors). Compose moved it to an internal package, so it can no
// longer be imported. It must stay byte-identical to what Compose stamps in
// the com.docker.compose.config-hash label, or every service will be seen as
// changed on each deploy.

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/opencontainers/go-digest"
)

// serviceHashKeyOrder freezes the top-level JSON key order of the service
// config hash, matching the layout Compose uses
var serviceHashKeyOrder = []string{
	"profiles",
	"annotations",
	"attach",
	"build",
	"develop",
	"blkio_config",
	"cap_add",
	"cap_drop",
	"cgroup_parent",
	"cgroup",
	"cpu_count",
	"cpu_percent",
	"cpu_period",
	"cpu_quota",
	"cpu_rt_period",
	"cpu_rt_runtime",
	"cpus",
	"cpuset",
	"cpu_shares",
	"command",
	"configs",
	"container_name",
	"credential_spec",
	"depends_on",
	"deploy",
	"device_cgroup_rules",
	"devices",
	"dns",
	"dns_opt",
	"dns_search",
	"dockerfile",
	"domainname",
	"entrypoint",
	"provider",
	"environment",
	"env_file",
	"expose",
	"extends",
	"external_links",
	"extra_hosts",
	"group_add",
	"gpus",
	"hostname",
	"healthcheck",
	"image",
	"init",
	"ipc",
	"isolation",
	"labels",
	"label_file",
	"links",
	"logging",
	"log_driver",
	"log_opt",
	"mem_limit",
	"mem_reservation",
	"memswap_limit",
	"mem_swappiness",
	"mac_address",
	"models",
	"net",
	"network_mode",
	"networks",
	"oom_kill_disable",
	"oom_score_adj",
	"pid",
	"pids_limit",
	"platform",
	"ports",
	"privileged",
	"pull_policy",
	"read_only",
	"restart",
	"runtime",
	"scale",
	"secrets",
	"security_opt",
	"shm_size",
	"stdin_open",
	"stop_grace_period",
	"stop_signal",
	"storage_opt",
	"sysctls",
	"tmpfs",
	"tty",
	"ulimits",
	"use_api_socket",
	"user",
	"userns_mode",
	"uts",
	"volume_driver",
	"volumes",
	"volumes_from",
	"working_dir",
	"pre_start",
	"post_start",
	"pre_stop",
	"pull_refresh_after",
	"umask",
}

// trimServiceHashFields removes the attributes Compose excludes from the
// service config hash
func trimServiceHashFields(o types.ServiceConfig) types.ServiceConfig {
	o.Build = nil
	o.PullPolicy = ""
	o.Scale = nil
	if o.Deploy != nil {
		deploy := *o.Deploy
		deploy.Replicas = nil
		o.Deploy = &deploy
	}
	o.DependsOn = nil
	o.Profiles = nil
	return o
}

// serviceHash computes the configuration hash for a service
func serviceHash(o types.ServiceConfig) (string, error) {
	o = trimServiceHashFields(o)

	raw, err := json.Marshal(o)
	if err != nil {
		return "", err
	}
	pinned, err := pinRootKeyOrder(raw, serviceHashKeyOrder)
	if err != nil {
		return "", err
	}
	return digest.SHA256.FromBytes(pinned).Encoded(), nil
}

// pinRootKeyOrder re-emits a JSON object with its top-level keys in the
// given order, appending keys absent from the list in sorted order
func pinRootKeyOrder(raw []byte, order []string) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	write := func(key string, val json.RawMessage) {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		name, _ := json.Marshal(key)
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(val)
	}
	for _, key := range order {
		if val, ok := root[key]; ok {
			write(key, val)
			delete(root, key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(root)) {
		write(key, root[key])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
