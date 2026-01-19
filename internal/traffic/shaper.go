package traffic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vishvananda/netlink"

	"tcsss/internal/config"
	"tcsss/internal/infra"
	route "tcsss/internal/route"
)

const (
	// IfbPrefix is the prefix used for IFB interface names. tc limits names to 15 chars.
	IfbPrefix = "ifb4"
	// IngressHandle is the tc handle identifier reserved for ingress qdiscs.
	IngressHandle = "ffff:"
	// defaultWorkerCount limits concurrent interface configuration to a small, safe pool.
	defaultWorkerCount = 4
	// maxSignatureEntries bounds the applied signature cache to avoid unbounded growth on interface churn.
	maxSignatureEntries = 256
)

// Shaper orchestrates traffic shaping for network interfaces.
type Shaper struct {
	logger            *slog.Logger
	routeOptimizer    *route.Optimizer
	classifier        *InterfaceClassifier
	appliedMu         sync.RWMutex
	appliedSignatures map[string]string
	didInitialCleanup bool
	netlink           infra.NetlinkClient
	executor          infra.CommandExecutor
	reapplyInterval   time.Duration
	cleanupInterval   time.Duration
	applyTimeout      time.Duration
	profiles          profileSet
}

// NewShaper constructs a traffic Shaper.
func NewShaper(logger *slog.Logger, settings Settings) *Shaper {
	return NewShaperWithDependencies(logger, settings, infra.DefaultNetlinkClient{}, infra.ProcessExecutor{})
}

// NewShaperWithDependencies constructs a traffic Shaper with injected dependencies.
func NewShaperWithDependencies(logger *slog.Logger, settings Settings, netlinkClient infra.NetlinkClient, executor infra.CommandExecutor) *Shaper {
	settings = settings.withDefaults()
	return &Shaper{
		logger: logger,
		routeOptimizer: route.NewOptimizer(logger, settings.Routes, route.Dependencies{
			Netlink:        netlinkClient,
			Executor:       executor,
			CommandTimeout: 0,
		}),
		classifier:        NewInterfaceClassifier(logger, netlinkClient),
		appliedSignatures: make(map[string]string),
		netlink:           netlinkClient,
		executor:          executor,
		reapplyInterval:   settings.Watcher.ReapplyInterval,
		cleanupInterval:   settings.Watcher.CleanupInterval,
		applyTimeout:      settings.Watcher.ApplyTimeout,
		profiles:          newProfileSet(settings.Profiles),
	}
}

// Apply configures traffic shaping for all relevant interfaces.
func (s *Shaper) Apply(ctx context.Context) error {
	// First, optimize routing tables for better TCP performance
	if err := s.routeOptimizer.Optimize(ctx); err != nil {
		s.logError("route optimization failed", "", err, slog.String("operation", "optimize_routes"))
		// Continue with traffic shaping even if route optimization fails
	}

	return s.applyInterfaces(ctx, nil)
}

func (s *Shaper) recordSignature(iface, signature string) {
	if iface == "" {
		return
	}

	s.appliedMu.Lock()
	defer s.appliedMu.Unlock()

	if s.appliedSignatures == nil {
		s.appliedSignatures = make(map[string]string)
	}

	s.appliedSignatures[iface] = signature
	if len(s.appliedSignatures) <= maxSignatureEntries {
		return
	}

	evictCount := maxSignatureEntries / 10
	if evictCount < 1 {
		evictCount = 1
	}

	deleted := 0
	for k := range s.appliedSignatures {
		if k == iface {
			continue
		}
		delete(s.appliedSignatures, k)
		deleted++
		if deleted >= evictCount {
			break
		}
	}
}

// makeSignature creates a lightweight signature describing desired state to avoid redundant tc/ethtool calls.
func (s *Shaper) makeSignature(mtu, qlen string, profile shapingProfile) string {
	var b strings.Builder
	b.WriteString("mtu=")
	b.WriteString(mtu)
	b.WriteString(";qlen=")
	b.WriteString(qlen)
	b.WriteString(";root=")
	b.WriteString(strings.Join(profile.rootQdisc, ","))
	b.WriteString(";ifb=")
	b.WriteString(strings.Join(profile.ifbQdisc, ","))
	b.WriteString(";off=")
	// Sort offloads for stable signature
	if len(profile.offloads) > 0 {
		pairs := make([]string, 0, len(profile.offloads))
		for _, o := range profile.offloads {
			pairs = append(pairs, fmt.Sprintf("%s=%s", normalizeSetFeatureName(o.feature), strings.ToLower(o.state)))
		}
		sort.Strings(pairs)
		b.WriteString(strings.Join(pairs, ","))
	}
	return b.String()
}

type profileContext struct {
	iface           string
	attrs           *netlink.LinkAttrs
	profile         shapingProfile
	profileName     string
	mtuStr          string
	queueLength     string
	desiredMTU      int
	desiredQueueLen int
	signature       string
	ifbName         string
}

type profileStep func(context.Context, *profileContext) error

// applyInterfaces applies shaping to either all interfaces (only == nil) or the provided set of names.
// This is the main entry point for applying traffic shaping configuration.
func (s *Shaper) applyInterfaces(ctx context.Context, only map[string]struct{}) error {
	links, err := s.listAndPrepareLinks(ctx)
	if err != nil {
		return err
	}

	s.ensureInitialCleanup(ctx, links)

	requiredIfbsAll := s.determineRequiredIfbs(links)
	if err := s.applyToLinks(ctx, links, only); err != nil {
		s.logError("interface configuration encountered errors", "", err)
	}

	if err := s.pruneStaleIfbs(ctx, links, requiredIfbsAll); err != nil {
		s.logError("prune ifb failed", "", err)
	}

	return nil
}

// listAndPrepareLinks fetches network links and refreshes interface classification cache.
func (s *Shaper) listAndPrepareLinks(ctx context.Context) ([]netlink.Link, error) {
	links, err := s.netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}

	if err := s.classifier.RefreshExternalInterfaces(); err != nil && s.logger != nil {
		s.logger.Warn("failed to refresh external interface cache", slog.String("error", err.Error()))
	}

	return links, nil
}

type workerStats struct {
	processed int
	failed    int
}

func (s *Shaper) applyToLinks(ctx context.Context, links []netlink.Link, only map[string]struct{}) error {
	if len(links) == 0 {
		return nil
	}

	workerCount := s.workerCount(len(links))
	workCh := make(chan netlink.Link, len(links))
	errCh := make(chan error, len(links))
	statsCh := make(chan workerStats, workerCount)

	var wg sync.WaitGroup
	s.startLinkWorkers(ctx, workerCount, &wg, workCh, errCh, statsCh, only)

	for _, link := range links {
		workCh <- link
	}
	close(workCh)

	wg.Wait()
	close(errCh)
	close(statsCh)

	return s.summarizeLinkResults(errCh, statsCh)
}

func (s *Shaper) configureProfile(ctx context.Context, attrs *netlink.LinkAttrs, profile shapingProfile, profileName string) error {
	profileCtx, skip, err := s.buildProfileContext(attrs, profile, profileName)
	if err != nil || skip {
		return err
	}

	steps := []profileStep{
		s.configureLinkParamsStep,
		s.configureRootQdiscStep,
		s.configureIngressAndIfbStep,
		s.ensureOffloadsStep,
	}

	if err := s.runProfileSteps(ctx, profileCtx, steps); err != nil {
		return err
	}

	s.recordSignature(profileCtx.iface, profileCtx.signature)
	return nil
}

// buildProfileContext constructs configuration context for an interface profile.
// Returns (context, skip, error) where skip=true indicates the interface is already configured.
func (s *Shaper) buildProfileContext(attrs *netlink.LinkAttrs, profile shapingProfile, profileName string) (*profileContext, bool, error) {
	if err := s.validateProfileInput(attrs, profileName); err != nil {
		return nil, false, err
	}

	iface := attrs.Name
	mtuStr, queueLength := deriveProfileParameters(attrs, profile)
	signature := s.makeSignature(mtuStr, queueLength, profile)

	if s.isAlreadyConfigured(iface, signature) {
		return nil, true, nil
	}

	desiredMTU, desiredQueueLen, err := s.parseProfileParameters(iface, mtuStr, queueLength, profileName)
	if err != nil {
		return nil, false, err
	}

	return &profileContext{
		iface:           iface,
		attrs:           attrs,
		profile:         profile,
		profileName:     profileName,
		mtuStr:          mtuStr,
		queueLength:     queueLength,
		desiredMTU:      desiredMTU,
		desiredQueueLen: desiredQueueLen,
		signature:       signature,
		ifbName:         truncateIfb(IfbPrefix + iface),
	}, false, nil
}

// parseProfileParameters validates and converts MTU and queue length strings to integers.
func (s *Shaper) parseProfileParameters(iface, mtuStr, queueLength, profileName string) (int, int, error) {
	desiredMTU, err := strconv.Atoi(mtuStr)
	if err != nil {
		return 0, 0, fmt.Errorf("parse mtu %q for %s (%s): %w", mtuStr, iface, profileName, err)
	}

	if desiredMTU < config.MinMTU || desiredMTU > config.MaxMTU {
		return 0, 0, fmt.Errorf("mtu %d for %s (%s) out of range [%d, %d]", desiredMTU, iface, profileName, config.MinMTU, config.MaxMTU)
	}

	desiredQueueLen, err := strconv.Atoi(queueLength)
	if err != nil {
		return 0, 0, fmt.Errorf("parse qlen %q for %s (%s): %w", queueLength, iface, profileName, err)
	}

	if desiredQueueLen < config.MinQueueLen || desiredQueueLen > config.MaxQueueLen {
		return 0, 0, fmt.Errorf("queue length %d for %s (%s) out of range [%d, %d]", desiredQueueLen, iface, profileName, config.MinQueueLen, config.MaxQueueLen)
	}

	return desiredMTU, desiredQueueLen, nil
}

func (s *Shaper) runProfileSteps(ctx context.Context, profileCtx *profileContext, steps []profileStep) error {
	for _, step := range steps {
		if err := step(ctx, profileCtx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Shaper) startLinkWorkers(
	ctx context.Context,
	workerCount int,
	wg *sync.WaitGroup,
	workCh <-chan netlink.Link,
	errCh chan<- error,
	statsCh chan<- workerStats,
	only map[string]struct{},
) {
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go s.linkWorker(ctx, wg, workCh, errCh, statsCh, only)
	}
}

func (s *Shaper) linkWorker(
	ctx context.Context,
	wg *sync.WaitGroup,
	workCh <-chan netlink.Link,
	errCh chan<- error,
	statsCh chan<- workerStats,
	only map[string]struct{},
) {
	defer wg.Done()

	stats := workerStats{}
	for link := range workCh {
		processed, err := s.processLink(ctx, link, only)
		if !processed {
			continue
		}
		stats.processed++
		if err != nil {
			stats.failed++
			errCh <- err
		}
	}

	statsCh <- stats
}

func (s *Shaper) processLink(ctx context.Context, link netlink.Link, only map[string]struct{}) (bool, error) {
	attrs := link.Attrs()
	name, shouldProcess := s.shouldProcessLink(attrs, only)
	if !shouldProcess {
		return false, nil
	}

	class := s.classifier.Classify(attrs)
	switch class {
	case classLoopback:
		return true, s.applyProfile(ctx, name, attrs, s.profiles.loopback, "loopback", "loopback configure failed")
	case classExternalPhysical:
		return true, s.applyProfile(ctx, name, attrs, s.profiles.externalPhysical, "external-physical", "external physical configure failed")
	case classExternalVirtual:
		return true, s.applyProfile(ctx, name, attrs, s.profiles.externalVirtual, "external-virtual", "external virtual configure failed")
	case classInternalVirtual:
		return true, s.applyProfile(ctx, name, attrs, s.profiles.internalVirtual, "internal-virtual", "internal virtual configure failed")
	case classInternalVirtualSkip:
		if s.logger != nil {
			s.logger.Debug("skipping internal virtual interface", slog.String("interface", name))
		}
		return true, nil
	default:
		if s.logger != nil {
			s.logger.Warn("unknown interface classification", slog.String("interface", name))
		}
		return true, nil
	}
}

func (s *Shaper) applyProfile(
	ctx context.Context,
	iface string,
	attrs *netlink.LinkAttrs,
	profile shapingProfile,
	profileName string,
	errorMessage string,
) error {
	err := s.configureProfile(ctx, attrs, profile, profileName)
	if err != nil {
		s.logError(errorMessage, iface, err, slog.String("profile", profileName))
	}
	return err
}

func (s *Shaper) summarizeLinkResults(errCh <-chan error, statsCh <-chan workerStats) error {
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	total := workerStats{}
	for stats := range statsCh {
		total.processed += stats.processed
		total.failed += stats.failed
	}

	if len(errs) > 0 {
		if s.logger != nil {
			s.logger.Warn("some interfaces failed",
				slog.Int("failed", total.failed),
				slog.Int("processed", total.processed))
		}
		return fmt.Errorf("apply links: %w", errors.Join(errs...))
	}
	return nil
}

func (s *Shaper) workerCount(total int) int {
	switch {
	case total <= 4:
		return total
	case total <= 16:
		return defaultWorkerCount
	case total <= 64:
		return 8
	default:
		wc := minInt(16, runtime.NumCPU())
		if wc < 1 {
			wc = 1
		}
		return wc
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Shaper) shouldProcessLink(attrs *netlink.LinkAttrs, only map[string]struct{}) (string, bool) {
	if attrs == nil {
		return "", false
	}
	name := attrs.Name
	if name == "" || strings.HasPrefix(name, "ifb") {
		return "", false
	}
	if only != nil {
		if _, ok := only[name]; !ok {
			return name, false
		}
	}
	return name, true
}

func (s *Shaper) validateProfileInput(attrs *netlink.LinkAttrs, profileName string) error {
	if attrs == nil {
		return fmt.Errorf("link attrs missing for profile %s", profileName)
	}
	if attrs.Name == "" {
		return fmt.Errorf("link name missing for profile %s", profileName)
	}
	return nil
}

// deriveProfileParameters extracts MTU and queue length values from interface attributes and profile.
// Falls back to profile defaults if specific values are not set.
func deriveProfileParameters(attrs *netlink.LinkAttrs, profile shapingProfile) (string, string) {
	mtuStr := fmt.Sprintf("%d", attrs.MTU)
	if profile.mtuOverride != "" {
		mtuStr = profile.mtuOverride
	}
	queueLength := profile.queueLength
	return mtuStr, queueLength
}

func (s *Shaper) isAlreadyConfigured(iface, sig string) bool {
	s.appliedMu.RLock()
	defer s.appliedMu.RUnlock()

	prev, ok := s.appliedSignatures[iface]
	if !ok || prev != sig {
		return false
	}

	ifbName := truncateIfb(IfbPrefix + iface)
	link, err := s.netlink.LinkByName(ifbName)
	if err != nil || link == nil {
		return false
	}

	attrs := link.Attrs()
	return attrs != nil && (attrs.Flags&net.FlagUp) != 0
}

func (s *Shaper) configureLinkParamsStep(ctx context.Context, pc *profileContext) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if pc.attrs.MTU == pc.desiredMTU && pc.attrs.TxQLen == pc.desiredQueueLen {
		return nil
	}
	link, err := s.netlink.LinkByName(pc.iface)
	if err != nil {
		return fmt.Errorf("lookup link %s: %w", pc.iface, err)
	}

	if pc.attrs.MTU != pc.desiredMTU {
		if err := s.netlink.LinkSetMTU(link, pc.desiredMTU); err != nil {
			return fmt.Errorf("set mtu %d for %s: %w", pc.desiredMTU, pc.iface, err)
		}
	}

	if pc.attrs.TxQLen != pc.desiredQueueLen {
		if err := s.netlink.LinkSetTxQLen(link, pc.desiredQueueLen); err != nil {
			return fmt.Errorf("set tx queue len %d for %s: %w", pc.desiredQueueLen, pc.iface, err)
		}
	}
	return nil
}

func (s *Shaper) configureRootQdiscStep(ctx context.Context, pc *profileContext) error {
	if len(pc.profile.rootQdisc) == 0 {
		return nil
	}
	qdisc := rootQdiscConfig(pc.iface, pc.profile.rootQdisc)
	if err := s.run(ctx, "tc", qdisc.ReplaceArgs()...); err != nil {
		return fmt.Errorf("configure root qdisc for %s: %w", pc.iface, err)
	}
	return nil
}

func (s *Shaper) configureIngressAndIfbStep(ctx context.Context, pc *profileContext) error {
	ingress := ingressQdiscConfig(pc.iface)
	if err := s.run(ctx, "tc", ingress.ReplaceArgs()...); err != nil {
		return fmt.Errorf("configure ingress qdisc for %s: %w", pc.iface, err)
	}

	if err := s.ensureIfb(ctx, pc.ifbName, pc.mtuStr, pc.queueLength); err != nil {
		return fmt.Errorf("ensure ifb %s for %s: %w", pc.ifbName, pc.iface, err)
	}

	if len(pc.profile.ifbQdisc) > 0 {
		ifbRoot := ifbRootQdiscConfig(pc.ifbName, pc.profile.ifbQdisc)
		if err := s.run(ctx, "tc", ifbRoot.ReplaceArgs()...); err != nil {
			return fmt.Errorf("configure ifb root qdisc %s: %w", pc.ifbName, err)
		}
	}

	filter := FilterConfig{
		Device:   pc.iface,
		Parent:   IngressHandle,
		Protocol: "all",
		Pref:     "1",
		Kind:     "matchall",
		Actions:  []string{"action", "mirred", "egress", "redirect", "dev", pc.ifbName},
	}
	if err := s.replaceFilter(ctx, filter); err != nil {
		return fmt.Errorf("replace filter for %s -> %s: %w", pc.iface, pc.ifbName, err)
	}

	return nil
}

func (s *Shaper) ensureOffloadsStep(ctx context.Context, pc *profileContext) error {
	s.ensureOffloads(ctx, pc.iface, pc.profile.offloads)
	return nil
}

func (s *Shaper) ensureInitialCleanup(ctx context.Context, links []netlink.Link) {
	if s.didInitialCleanup {
		return
	}
	if err := s.cleanupSkippedVirtualInterfaces(ctx, links); err != nil {
		s.logError("cleanup skipped virtual interfaces failed", "", err)
	}
	s.didInitialCleanup = true
}

func (s *Shaper) determineRequiredIfbs(links []netlink.Link) map[string]struct{} {
	required := map[string]struct{}{}
	for _, link := range links {
		attrs := link.Attrs()
		if attrs == nil {
			continue
		}
		name := attrs.Name
		if name == "" || strings.HasPrefix(name, "ifb") {
			continue
		}
		class := s.classifier.Classify(attrs)
		switch class {
		case classLoopback, classExternalPhysical, classExternalVirtual, classInternalVirtual:
			// These classes need IFB devices for ingress shaping
			required[truncateIfb(IfbPrefix+name)] = struct{}{}
		case classInternalVirtualSkip:
			// Internal virtual interfaces with skip prefixes are ignored
			continue
		}
	}
	return required
}

func (s *Shaper) cleanupStaleSignatures() error {
	links, err := s.netlink.LinkList()
	if err != nil {
		return fmt.Errorf("list links for signature cleanup: %w", err)
	}

	current := make(map[string]struct{}, len(links))
	for _, link := range links {
		if attrs := link.Attrs(); attrs != nil && attrs.Name != "" {
			current[attrs.Name] = struct{}{}
		}
	}

	s.appliedMu.Lock()
	for name := range s.appliedSignatures {
		if _, exists := current[name]; !exists {
			delete(s.appliedSignatures, name)
		}
	}
	s.appliedMu.Unlock()

	return nil
}

func (s *Shaper) logError(message, iface string, err error, attrs ...slog.Attr) {
	if s.logger == nil || err == nil {
		return
	}

	all := make([]slog.Attr, 0, len(attrs)+2)
	all = append(all, slog.String("error", err.Error()))
	if iface != "" {
		all = append(all, slog.String("interface", iface))
	}
	all = append(all, attrs...)

	s.logger.LogAttrs(context.Background(), slog.LevelError, message, all...)
}

func (s *Shaper) logOptional(message, iface string, err error, attrs ...slog.Attr) {
	if s.logger == nil || err == nil {
		return
	}

	all := make([]slog.Attr, 0, len(attrs)+2)
	all = append(all, slog.String("error", err.Error()))
	if iface != "" {
		all = append(all, slog.String("interface", iface))
	}
	all = append(all, attrs...)

	s.logger.LogAttrs(context.Background(), slog.LevelDebug, message, all...)
}
