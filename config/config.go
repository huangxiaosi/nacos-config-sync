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
}

type HostSection struct {
	Name        string
	NamespaceID string
	Group       string
	Path        string
	DataIDs     []string
}

type HostConfig struct {
	Common   HostSection
	Sections []HostSection
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
		Name:        "common",
		NamespaceID: commonNamespaceID,
		Group:       commonGroup,
		DataIDs:     commonDataIDs,
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
			Name:        name,
			NamespaceID: strings.TrimSpace(sec.Key("namespaceId").String()),
			Group:       group,
			Path:        strings.TrimSpace(sec.Key("path").String()),
			DataIDs:     dataIDs,
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
