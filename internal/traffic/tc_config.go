package traffic

// QdiscConfig describes a traffic control qdisc operation.
type QdiscConfig struct {
	Device  string
	Root    bool
	Parent  string
	Handle  string
	Kind    string
	Options []string
}

// ReplaceArgs renders the tc arguments required to replace the qdisc.
func (qc QdiscConfig) ReplaceArgs() []string {
	args := []string{"qdisc", "replace", "dev", qc.Device}

	switch {
	case qc.Root:
		args = append(args, "root")
	case qc.Parent != "":
		args = append(args, "parent", qc.Parent)
	}

	if qc.Handle != "" {
		args = append(args, "handle", qc.Handle)
	}

	if qc.Kind != "" {
		args = append(args, qc.Kind)
	}
	if len(qc.Options) > 0 {
		args = append(args, qc.Options...)
	}
	return args
}

// FilterConfig holds tc filter parameters applied via replaceFilter.
type FilterConfig struct {
	Device   string
	Parent   string
	Protocol string
	Pref     string
	Kind     string
	Actions  []string
}

// DeleteArgs renders the tc arguments to delete an existing filter instance.
func (fc FilterConfig) DeleteArgs() []string {
	return []string{
		"filter", "del",
		"dev", fc.Device,
		"parent", fc.Parent,
		"protocol", fc.Protocol,
		"pref", fc.Pref,
	}
}

// AddArgs renders the tc arguments to add a filter.
func (fc FilterConfig) AddArgs() []string {
	args := []string{
		"filter", "add",
		"dev", fc.Device,
		"parent", fc.Parent,
		"protocol", fc.Protocol,
		"pref", fc.Pref,
		fc.Kind,
	}
	if len(fc.Actions) > 0 {
		args = append(args, fc.Actions...)
	}
	return args
}

func splitQdiscSpec(spec []string) (string, []string) {
	if len(spec) == 0 {
		return "", nil
	}
	kind := spec[0]
	if len(spec) == 1 {
		return kind, nil
	}
	options := make([]string, len(spec)-1)
	copy(options, spec[1:])
	return kind, options
}

func rootQdiscConfig(device string, spec []string) QdiscConfig {
	kind, options := splitQdiscSpec(spec)
	return QdiscConfig{
		Device:  device,
		Root:    true,
		Kind:    kind,
		Options: options,
	}
}

func ifbRootQdiscConfig(ifb string, spec []string) QdiscConfig {
	return rootQdiscConfig(ifb, spec)
}

func ingressQdiscConfig(device string) QdiscConfig {
	return QdiscConfig{
		Device: device,
		Handle: IngressHandle,
		Kind:   "ingress",
	}
}
