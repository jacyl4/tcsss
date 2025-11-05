package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/vishvananda/netlink"

	"tcsss/internal/config"
	terr "tcsss/internal/errors"
)

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
		s.handleCategorizedError("interface configuration encountered errors", "", err, terr.CategoryRecoverable)
	}

	if err := s.pruneStaleIfbs(ctx, links, requiredIfbsAll); err != nil {
		s.handleCategorizedError("prune ifb failed", "", err, terr.CategoryRecoverable)
	}

	return nil
}

// listAndPrepareLinks fetches network links and refreshes interface classification cache.
func (s *Shaper) listAndPrepareLinks(ctx context.Context) ([]netlink.Link, error) {
	links, err := s.netlink.LinkList()
	if err != nil {
		return nil, terr.New(
			terr.CategoryCritical,
			fmt.Errorf("list links: %w", err),
			terr.ErrorContext{Operation: "link_list"},
		)
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

	defer close(errCh)
	defer close(statsCh)

	var wg sync.WaitGroup
	s.startLinkWorkers(ctx, workerCount, &wg, workCh, errCh, statsCh, only)

	for _, link := range links {
		workCh <- link
	}
	close(workCh)

	wg.Wait()

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

	s.appliedMu.Lock()
	s.appliedSignatures[profileCtx.iface] = profileCtx.signature
	s.appliedMu.Unlock()
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
		return 0, 0, wrapInterfaceError(
			fmt.Errorf("parse mtu %q for %s: %w", mtuStr, iface, err),
			iface,
			"parse_profile_mtu",
			terr.ErrorContext{Profile: profileName, Value: mtuStr},
		)
	}

	if desiredMTU < config.MinMTU || desiredMTU > config.MaxMTU {
		return 0, 0, wrapInterfaceError(
			fmt.Errorf("mtu %d out of range [%d, %d]", desiredMTU, config.MinMTU, config.MaxMTU),
			iface,
			"validate_profile_mtu",
			terr.ErrorContext{Profile: profileName, Value: mtuStr},
		)
	}

	desiredQueueLen, err := strconv.Atoi(queueLength)
	if err != nil {
		return 0, 0, wrapInterfaceError(
			fmt.Errorf("parse qlen %q for %s: %w", queueLength, iface, err),
			iface,
			"parse_profile_queue",
			terr.ErrorContext{Profile: profileName, Value: queueLength},
		)
	}

	if desiredQueueLen < config.MinQueueLen || desiredQueueLen > config.MaxQueueLen {
		return 0, 0, wrapInterfaceError(
			fmt.Errorf("queue length %d out of range [%d, %d]", desiredQueueLen, config.MinQueueLen, config.MaxQueueLen),
			iface,
			"validate_profile_queue",
			terr.ErrorContext{Profile: profileName, Value: queueLength},
		)
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
		s.handleCategorizedError(errorMessage, iface, err, terr.CategoryRecoverable)
	}
	return err
}

func (s *Shaper) summarizeLinkResults(errCh <-chan error, statsCh <-chan workerStats) error {
	var errs terr.MultiError
	for err := range errCh {
		errs.Add(err)
	}

	total := workerStats{}
	for stats := range statsCh {
		total.processed += stats.processed
		total.failed += stats.failed
	}

	if final := errs.ErrorOrNil(); final != nil {
		if s.logger != nil {
			s.logger.Warn("some interfaces failed",
				slog.Int("failed", total.failed),
				slog.Int("processed", total.processed))
		}
		return terr.WrapRecoverable(final, "apply_links", terr.ErrorContext{
			Extra: map[string]any{
				"failed":    total.failed,
				"processed": total.processed,
			},
		})
	}
	return nil
}

func (s *Shaper) workerCount(total int) int {
	if total < defaultWorkerCount {
		return total
	}
	return defaultWorkerCount
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
		return terr.New(
			terr.CategoryRecoverable,
			fmt.Errorf("link attrs missing for profile"),
			terr.ErrorContext{Profile: profileName},
		)
	}
	if attrs.Name == "" {
		return terr.New(
			terr.CategoryRecoverable,
			fmt.Errorf("link name missing for profile"),
			terr.ErrorContext{Profile: profileName},
		)
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
