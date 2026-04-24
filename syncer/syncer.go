package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"nacos-config-sync/atomicfile"
	"nacos-config-sync/config"
	"nacos-config-sync/logger"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

type watchJob struct {
	NamespaceID string
	Group       string
	DataID      string
	Dirs        []string
}

type commonSkip struct {
	Path        string
	DataID      string
	NamespaceID string
	Group       string
}

type Syncer struct {
	log   *logger.Logger
	nacos *config.NacosConfig

	applyMu sync.Mutex
	mu      sync.Mutex
	stopped bool
	clients map[string]config_client.IConfigClient
	active  map[string]watchJob
}

func New(log *logger.Logger, nc *config.NacosConfig) (*Syncer, error) {
	baseClient, err := buildConfigClient(nc, nc.NamespaceID)
	if err != nil {
		return nil, err
	}

	clientMap := map[string]config_client.IConfigClient{
		nc.NamespaceID: baseClient,
	}

	return &Syncer{
		log:     log,
		nacos:   nc,
		clients: clientMap,
		active:  make(map[string]watchJob),
	}, nil
}

func buildConfigClient(nc *config.NacosConfig, namespaceID string) (config_client.IConfigClient, error) {
	if nc.RpcKeepAliveSeconds > 0 {
		_ = os.Setenv("NACOS_SDK_RPC_KEEP_ALIVE_SECONDS", strconv.FormatUint(nc.RpcKeepAliveSeconds, 10))
	}

	logDir := nc.LogDir
	if logDir == "" {
		logDir = filepath.Join(os.TempDir(), "nacos-config-sync", "log")
	}
	cacheDir := nc.CacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "nacos-config-sync", "cache")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir logDir %s failed: %w", logDir, err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir cacheDir %s failed: %w", cacheDir, err)
	}

	opts := []constant.ClientOption{
		constant.WithNamespaceId(namespaceID),
		constant.WithTimeoutMs(15000),
		constant.WithNotLoadCacheAtStart(true),
		constant.WithLogDir(logDir),
		constant.WithCacheDir(cacheDir),
		constant.WithLogLevel(nc.LogLevel),
	}
	if nc.Username != "" {
		opts = append(opts, constant.WithUsername(nc.Username), constant.WithPassword(nc.Password))
	}
	cc := *constant.NewClientConfig(opts...)

	sc := []constant.ServerConfig{
		*constant.NewServerConfig(nc.IPAddr, nc.Port, constant.WithContextPath("/nacos")),
	}

	client, err := clients.NewConfigClient(
		vo.NacosClientParam{
			ClientConfig:  &cc,
			ServerConfigs: sc,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create nacos config client: %w", err)
	}
	return client, nil
}

func (s *Syncer) Run(ctx context.Context) error {
	hostDataID := s.nacos.HostID + ".ini"
	baseClient, err := s.getOrCreateClient(s.nacos.NamespaceID)
	if err != nil {
		return err
	}
	raw, err := baseClient.GetConfig(vo.ConfigParam{
		DataId: hostDataID,
		Group:  s.nacos.Group,
	})
	if err != nil {
		return fmt.Errorf("get host profile %s (group=%s): %w", hostDataID, s.nacos.Group, err)
	}

	hostCfg, err := config.ParseHostConfigFromContent(raw)
	if err != nil {
		return fmt.Errorf("parse host profile: %w", err)
	}
	s.log.Info("host profile loaded", map[string]interface{}{
		"hostIni": hostDataID,
		"group":   s.nacos.Group,
	})
	s.applyHostConfig(hostCfg, "initial")

	if err := baseClient.ListenConfig(vo.ConfigParam{
		DataId: hostDataID,
		Group:  s.nacos.Group,
		OnChange: func(namespace, group, dataId, data string) {
			if s.isStopped() {
				return
			}
			cfg, parseErr := config.ParseHostConfigFromContent(data)
			if parseErr != nil {
				s.log.Error("host profile parse failed", map[string]interface{}{
					"hostIni": hostDataID,
					"error":   parseErr.Error(),
				})
				return
			}
			s.log.Info("host profile changed", map[string]interface{}{
				"hostIni": hostDataID,
				"group":   s.nacos.Group,
			})
			s.applyHostConfig(cfg, "host_profile_change")
		},
	}); err != nil {
		return fmt.Errorf("listen host profile failed: %w", err)
	}
	s.log.Info("host profile listener registered", map[string]interface{}{
		"hostIni": hostDataID,
		"group":   s.nacos.Group,
	})

	<-ctx.Done()
	return context.Canceled
}

func ensureModulePaths(h *config.HostConfig) error {
	var firstErr error
	seen := make(map[string]struct{})
	for _, sec := range h.Sections {
		p := filepath.Clean(sec.Path)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		if err := os.MkdirAll(p, 0o755); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("mkdir %s failed: %w", p, err)
		}
	}
	return firstErr
}

func (s *Syncer) Stop() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()

	hostDataID := s.nacos.HostID + ".ini"
	baseClient := s.clients[s.nacos.NamespaceID]
	if baseClient != nil {
		_ = baseClient.CancelListenConfig(vo.ConfigParam{
			DataId: hostDataID,
			Group:  s.nacos.Group,
		})
	}

	for _, p := range s.active {
		client := s.clients[p.NamespaceID]
		if client == nil {
			continue
		}
		_ = client.CancelListenConfig(vo.ConfigParam{
			DataId: p.DataID,
			Group:  p.Group,
		})
	}
	for _, client := range s.clients {
		client.CloseClient()
	}
}

func (s *Syncer) pullAndWrite(job *watchJob) error {
	client, err := s.getOrCreateClient(job.NamespaceID)
	if err != nil {
		return err
	}
	content, err := client.GetConfig(vo.ConfigParam{
		DataId: job.DataID,
		Group:  job.Group,
	})
	if err != nil {
		return fmt.Errorf("get config namespace=%s dataId=%s group=%s: %w", job.NamespaceID, job.DataID, job.Group, err)
	}
	for _, dir := range job.Dirs {
		if err := atomicfile.Write(dir, job.DataID, content); err != nil {
			return fmt.Errorf("write dataId=%s to %s: %w", job.DataID, dir, err)
		}
	}
	s.log.Info("config pulled", map[string]interface{}{
		"namespace": job.NamespaceID,
		"dataId":    job.DataID,
		"group":     job.Group,
		"dirs":      job.Dirs,
	})
	return nil
}

func (s *Syncer) listen(job *watchJob) error {
	j := job
	client, err := s.getOrCreateClient(j.NamespaceID)
	if err != nil {
		return err
	}
	err = client.ListenConfig(vo.ConfigParam{
		DataId: j.DataID,
		Group:  j.Group,
		OnChange: func(namespace, group, dataId, data string) {
			for _, dir := range j.Dirs {
				if err := atomicfile.Write(dir, dataId, data); err != nil {
					s.log.Error("write on change failed", map[string]interface{}{
						"namespace": namespace,
						"group":     group,
						"dataId":    dataId,
						"dir":       dir,
						"error":     err.Error(),
					})
					continue
				}
			}
			s.log.Info("config updated", map[string]interface{}{
				"namespace": namespace,
				"group":     group,
				"dataId":    dataId,
				"dirs":      j.Dirs,
			})
		},
	})
	if err != nil {
		return fmt.Errorf("listen namespace=%s dataId=%s group=%s: %w", j.NamespaceID, j.DataID, j.Group, err)
	}

	return nil
}

func buildWatchJobs(h *config.HostConfig) []*watchJob {
	key := func(namespaceID, group, dataID string) string {
		return namespaceID + "\x00" + group + "\x00" + dataID
	}
	byKey := make(map[string]*watchJob)

	modulePaths := make([]string, 0, len(h.Sections))
	seenPath := make(map[string]struct{})
	// Track module-owned dataIds per output path.
	// If common has the same dataId, module definition should win for that path.
	pathOwnedDataIDs := make(map[string]map[string]struct{})
	for _, sec := range h.Sections {
		p := filepath.Clean(sec.Path)
		if _, ok := seenPath[p]; ok {
			// keep collecting owned dataIds even when path repeats
		} else {
			seenPath[p] = struct{}{}
			modulePaths = append(modulePaths, p)
		}
		owned := pathOwnedDataIDs[p]
		if owned == nil {
			owned = make(map[string]struct{})
			pathOwnedDataIDs[p] = owned
		}
		for _, id := range sec.DataIDs {
			owned[id] = struct{}{}
		}
	}

	for _, sec := range h.Sections {
		for _, id := range sec.DataIDs {
			k := key(sec.NamespaceID, sec.Group, id)
			j, ok := byKey[k]
			if !ok {
				j = &watchJob{NamespaceID: sec.NamespaceID, Group: sec.Group, DataID: id}
				byKey[k] = j
			}
			addDirUnique(&j.Dirs, filepath.Clean(sec.Path))
		}
	}

	pathInherit := h.PathInheritsCommon()
	for _, id := range h.Common.DataIDs {
		k := key(h.Common.NamespaceID, h.Common.Group, id)
		j, ok := byKey[k]
		if !ok {
			j = &watchJob{NamespaceID: h.Common.NamespaceID, Group: h.Common.Group, DataID: id}
			byKey[k] = j
		}
		for _, p := range modulePaths {
			if !pathInherit[p] {
				continue
			}
			if owned := pathOwnedDataIDs[p]; owned != nil {
				if _, conflict := owned[id]; conflict {
					// Same filename exists in module config for this path, skip common copy.
					continue
				}
			}
			addDirUnique(&j.Dirs, p)
		}
	}

	out := make([]*watchJob, 0, len(byKey))
	for _, j := range byKey {
		sort.Strings(j.Dirs)
		out = append(out, j)
	}
	return out
}

func addDirUnique(dirs *[]string, d string) {
	for _, x := range *dirs {
		if x == d {
			return
		}
	}
	*dirs = append(*dirs, d)
}

func collectCommonSkips(h *config.HostConfig) []commonSkip {
	skips := make([]commonSkip, 0)
	pathOwnedDataIDs := make(map[string]map[string]struct{})
	for _, sec := range h.Sections {
		p := filepath.Clean(sec.Path)
		owned := pathOwnedDataIDs[p]
		if owned == nil {
			owned = make(map[string]struct{})
			pathOwnedDataIDs[p] = owned
		}
		for _, id := range sec.DataIDs {
			owned[id] = struct{}{}
		}
	}

	pathInherit := h.PathInheritsCommon()
	seen := make(map[string]struct{})
	for _, sec := range h.Sections {
		p := filepath.Clean(sec.Path)
		if !pathInherit[p] {
			continue
		}
		owned := pathOwnedDataIDs[p]
		for _, id := range h.Common.DataIDs {
			if _, ok := owned[id]; !ok {
				continue
			}
			k := p + "\x00" + id
			if _, exists := seen[k]; exists {
				continue
			}
			seen[k] = struct{}{}
			skips = append(skips, commonSkip{
				Path:        p,
				DataID:      id,
				NamespaceID: h.Common.NamespaceID,
				Group:       h.Common.Group,
			})
		}
	}
	return skips
}

func (s *Syncer) getOrCreateClient(namespaceID string) (config_client.IConfigClient, error) {
	s.mu.Lock()
	client := s.clients[namespaceID]
	s.mu.Unlock()
	if client != nil {
		return client, nil
	}

	newClient, err := buildConfigClient(s.nacos, namespaceID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.clients[namespaceID]; existing != nil {
		newClient.CloseClient()
		return existing, nil
	}
	s.clients[namespaceID] = newClient
	return newClient, nil
}

func (s *Syncer) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

func (s *Syncer) applyHostConfig(hostCfg *config.HostConfig, source string) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	if err := ensureModulePaths(hostCfg); err != nil {
		s.log.Error("ensure module paths failed", map[string]interface{}{
			"source": source,
			"error":  err.Error(),
		})
	}

	for _, skip := range collectCommonSkips(hostCfg) {
		s.log.Info("common skipped by module override", map[string]interface{}{
			"path":      skip.Path,
			"dataId":    skip.DataID,
			"namespace": skip.NamespaceID,
			"group":     skip.Group,
		})
	}

	jobs := buildWatchJobs(hostCfg)
	next := make(map[string]watchJob, len(jobs))
	for _, j := range jobs {
		next[jobKey(j.NamespaceID, j.Group, j.DataID)] = *j
	}
	desiredFiles := make(map[string]struct{})
	for _, job := range next {
		for _, dir := range job.Dirs {
			desiredFiles[fileKey(dir, job.DataID)] = struct{}{}
		}
	}

	pullSuccess := 0
	listenSuccess := 0
	failures := 0
	removed := 0

	for key, old := range s.active {
		newJob, ok := next[key]
		if ok && sameDirs(old.Dirs, newJob.Dirs) {
			continue
		}
		client := s.clients[old.NamespaceID]
		if client != nil {
			_ = client.CancelListenConfig(vo.ConfigParam{
				DataId: old.DataID,
				Group:  old.Group,
			})
		}
		delete(s.active, key)
		s.deleteObsoleteFiles(old, newJob, ok, desiredFiles, source)
		if !ok {
			removed++
			s.log.Info("sync job removed", map[string]interface{}{
				"namespace": old.NamespaceID,
				"group":     old.Group,
				"dataId":    old.DataID,
			})
		}
	}

	for key, job := range next {
		old, existed := s.active[key]
		if existed && sameDirs(old.Dirs, job.Dirs) {
			continue
		}
		jobCopy := job
		if err := s.pullAndWrite(&jobCopy); err != nil {
			failures++
			s.log.Error("initial pull failed", map[string]interface{}{
				"namespace": job.NamespaceID,
				"group":     job.Group,
				"dataId":    job.DataID,
				"dirs":      job.Dirs,
				"error":     err.Error(),
			})
		} else {
			pullSuccess++
		}
		if err := s.listen(&jobCopy); err != nil {
			failures++
			s.log.Error("listen register failed", map[string]interface{}{
				"namespace": job.NamespaceID,
				"group":     job.Group,
				"dataId":    job.DataID,
				"error":     err.Error(),
			})
			continue
		}
		listenSuccess++
		s.active[key] = job
	}

	s.log.Info("syncer running", map[string]interface{}{
		"source":        source,
		"jobs":          len(next),
		"removed":       removed,
		"pullSuccess":   pullSuccess,
		"listenSuccess": listenSuccess,
		"failures":      failures,
		"listeners":     len(s.active),
	})
}

func jobKey(namespaceID, group, dataID string) string {
	return namespaceID + "\x00" + group + "\x00" + dataID
}

func sameDirs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func fileKey(dir, dataID string) string {
	return filepath.Join(filepath.Clean(dir), dataID)
}

func (s *Syncer) deleteObsoleteFiles(old watchJob, newJob watchJob, hasNew bool, desiredFiles map[string]struct{}, source string) {
	newDirs := make(map[string]struct{})
	if hasNew {
		for _, dir := range newJob.Dirs {
			newDirs[filepath.Clean(dir)] = struct{}{}
		}
	}

	for _, dir := range old.Dirs {
		cleanDir := filepath.Clean(dir)
		if _, stillBound := newDirs[cleanDir]; stillBound {
			continue
		}
		target := filepath.Join(cleanDir, old.DataID)
		if _, stillNeeded := desiredFiles[fileKey(cleanDir, old.DataID)]; stillNeeded {
			continue
		}
		if err := os.Remove(target); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			s.log.Error("remove obsolete file failed", map[string]interface{}{
				"source": source,
				"path":   target,
				"error":  err.Error(),
			})
			continue
		}
		s.log.Info("obsolete file removed", map[string]interface{}{
			"source":    source,
			"path":      target,
			"namespace": old.NamespaceID,
			"group":     old.Group,
			"dataId":    old.DataID,
		})
	}
}
