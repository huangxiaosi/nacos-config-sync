package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
)

type NacosConfig struct {
	IPAddr      string
	Port        uint64
	NamespaceID string
	Username    string
	Password    string
	Group       string
	HostID      string
	LogDir      string
	CacheDir    string
	LogLevel    string
	// RpcKeepAliveSeconds > 0 sets env NACOS_SDK_RPC_KEEP_ALIVE_SECONDS before SDK init (idle HealthCheckRequest interval). Zero: do not set env (default 5s unless env is already set).
	RpcKeepAliveSeconds uint64
}

type HostSection struct {
	Name        string
	NamespaceID string
	Group       string
	Path        string
	DataIDs     []string
	// InheritCommon: when true (default), this module's path receives [common] dataIds (unless overridden by same-name module dataId). When false, common is not synced to this path.
	InheritCommon bool
}

type HostConfig struct {
	Common   HostSection
	Sections []HostSection
}

// PathInheritsCommon reports whether [common] dataIds should be synced under each module path.
// If several sections share the same path, common is applied only when all of them have InheritCommon true.
func (h *HostConfig) PathInheritsCommon() map[string]bool {
	groups := make(map[string][]bool)
	for _, sec := range h.Sections {
		p := filepath.Clean(sec.Path)
		groups[p] = append(groups[p], sec.InheritCommon)
	}
	out := make(map[string]bool, len(groups))
	for p, flags := range groups {
		in := true
		for _, f := range flags {
			if !f {
				in = false
				break
			}
		}
		out[p] = in
	}
	return out
}

func LoadNacosConfig(workDir string) (*NacosConfig, error) {
	cfgPath := filepath.Join(workDir, "nacos.ini")
	iniFile, err := ini.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load nacos.ini failed: %w", err)
	}

	sec := iniFile.Section("nacos")
	portVal, err := strconv.ParseUint(strings.TrimSpace(sec.Key("port").String()), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse nacos.port failed: %w", err)
	}

	result := &NacosConfig{
		IPAddr:      strings.TrimSpace(sec.Key("ipAddr").String()),
		Port:        portVal,
		NamespaceID: strings.TrimSpace(sec.Key("namespaceId").String()),
		Username:    strings.TrimSpace(sec.Key("username").String()),
		Password:    strings.TrimSpace(sec.Key("password").String()),
		Group:       strings.TrimSpace(sec.Key("group").String()),
		HostID:      strings.TrimSpace(sec.Key("hostId").String()),
		LogDir:      strings.TrimSpace(sec.Key("logDir").String()),
		CacheDir:    strings.TrimSpace(sec.Key("cacheDir").String()),
		LogLevel:    strings.TrimSpace(sec.Key("logLevel").String()),
	}

	if raw := strings.TrimSpace(sec.Key("rpcKeepAliveSeconds").String()); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse nacos.rpcKeepAliveSeconds failed: %w", err)
		}
		result.RpcKeepAliveSeconds = v
	}

	if result.IPAddr == "" {
		return nil, fmt.Errorf("nacos.ipAddr is required")
	}
	if result.NamespaceID == "" {
		return nil, fmt.Errorf("nacos.namespaceId is required")
	}
	if result.Group == "" {
		return nil, fmt.Errorf("nacos.group is required")
	}
	if result.HostID == "" {
		return nil, fmt.Errorf("nacos.hostId is required")
	}
	if result.LogLevel == "" {
		result.LogLevel = "info"
	}

	return result, nil
}

func ParseHostConfigFromContent(content string) (*HostConfig, error) {
	iniFile, err := ini.Load([]byte(content))
	if err != nil {
		return nil, fmt.Errorf("parse host ini failed: %w", err)
	}

	result := &HostConfig{}
	sections := iniFile.Sections()
	commonSec := iniFile.Section("common")
	if commonSec == nil || commonSec.Name() == ini.DefaultSection {
		return nil, fmt.Errorf("host config missing [common]")
	}

	commonGroup := strings.TrimSpace(commonSec.Key("group").String())
	if commonGroup == "" {
		return nil, fmt.Errorf("section common missing group")
	}
	commonDataIDs := parseCSV(commonSec.Key("dataId").String())
	if len(commonDataIDs) == 0 {
		return nil, fmt.Errorf("section common missing dataId")
	}
	commonNamespaceID := strings.TrimSpace(commonSec.Key("namespaceId").String())
	if commonNamespaceID == "" {
		return nil, fmt.Errorf("section common missing namespaceId")
	}
	result.Common = HostSection{
		Name:          "common",
		NamespaceID:   commonNamespaceID,
		Group:         commonGroup,
		DataIDs:       commonDataIDs,
		InheritCommon: true,
	}

	for _, sec := range sections {
		name := sec.Name()
		if name == ini.DefaultSection || name == "common" {
			continue
		}

		group := strings.TrimSpace(sec.Key("group").String())
		if group == "" {
			return nil, fmt.Errorf("section %s missing group", name)
		}

		dataIDs := parseCSV(sec.Key("dataId").String())
		if len(dataIDs) == 0 {
			return nil, fmt.Errorf("section %s missing dataId", name)
		}

		item := HostSection{
			Name:          name,
			NamespaceID:   strings.TrimSpace(sec.Key("namespaceId").String()),
			Group:         group,
			Path:          strings.TrimSpace(sec.Key("path").String()),
			DataIDs:       dataIDs,
			InheritCommon: true,
		}
		if raw := strings.TrimSpace(sec.Key("inheritCommon").String()); raw != "" {
			v, err := parseBoolFlexible(raw)
			if err != nil {
				return nil, fmt.Errorf("section %s inheritCommon: %w", name, err)
			}
			item.InheritCommon = v
		}

		if item.NamespaceID == "" {
			item.NamespaceID = commonNamespaceID
		} else if item.NamespaceID != commonNamespaceID {
			return nil, fmt.Errorf("section %s namespaceId must equal common.namespaceId", name)
		}

		if item.Path == "" {
			return nil, fmt.Errorf("section %s missing path", name)
		}
		result.Sections = append(result.Sections, item)
	}

	if len(result.Sections) == 0 {
		return nil, fmt.Errorf("host config has no non-common sections")
	}

	return result, nil
}

func parseBoolFlexible(raw string) (bool, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "true", "1", "yes", "y", "on":
		return true, nil
	case "false", "0", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("expected true/false/1/0/yes/no/on/off, got %q", raw)
	}
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
