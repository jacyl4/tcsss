package traffic

const (
	// IfbPrefix is the prefix used for IFB interface names. tc limits names to 15 chars.
	IfbPrefix = "ifb4"
	// IngressHandle is the tc handle identifier reserved for ingress qdiscs.
	IngressHandle = "ffff:"
	// defaultWorkerCount limits concurrent interface configuration to a small, safe pool.
	defaultWorkerCount = 4
)
