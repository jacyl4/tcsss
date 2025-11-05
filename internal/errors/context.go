package errors

const (
	contextKeyOperation = "operation"
	contextKeyInterface = "interface"
	contextKeyProfile   = "profile"
	contextKeyCommand   = "command"
	contextKeyIFB       = "ifb"
	contextKeyValue     = "value"
	contextKeyExpected  = "expected"
	contextKeyActual    = "actual"
)

// ErrorContext captures structured metadata for categorized errors.
type ErrorContext struct {
	Operation string
	Interface string
	Profile   string
	Command   string
	IFB       string
	Value     string
	Expected  string
	Actual    string
	Extra     map[string]any
}

// Merge returns a new ErrorContext combining the receiver with the provided context.
// Non-empty fields from the other context override existing values. Extra maps are merged.
func (ec ErrorContext) Merge(other ErrorContext) ErrorContext {
	result := ec

	if other.Operation != "" {
		result.Operation = other.Operation
	}
	if other.Interface != "" {
		result.Interface = other.Interface
	}
	if other.Profile != "" {
		result.Profile = other.Profile
	}
	if other.Command != "" {
		result.Command = other.Command
	}
	if other.IFB != "" {
		result.IFB = other.IFB
	}
	if other.Value != "" {
		result.Value = other.Value
	}
	if other.Expected != "" {
		result.Expected = other.Expected
	}
	if other.Actual != "" {
		result.Actual = other.Actual
	}

	if len(other.Extra) > 0 {
		if result.Extra == nil {
			result.Extra = make(map[string]any, len(other.Extra))
		}
		for k, v := range other.Extra {
			result.Extra[k] = v
		}
	}

	return result
}

// ToMap converts the context into a map for logging compatibility.
func (ec ErrorContext) ToMap() map[string]any {
	result := make(map[string]any)

	if ec.Operation != "" {
		result[contextKeyOperation] = ec.Operation
	}
	if ec.Interface != "" {
		result[contextKeyInterface] = ec.Interface
	}
	if ec.Profile != "" {
		result[contextKeyProfile] = ec.Profile
	}
	if ec.Command != "" {
		result[contextKeyCommand] = ec.Command
	}
	if ec.IFB != "" {
		result[contextKeyIFB] = ec.IFB
	}
	if ec.Value != "" {
		result[contextKeyValue] = ec.Value
	}
	if ec.Expected != "" {
		result[contextKeyExpected] = ec.Expected
	}
	if ec.Actual != "" {
		result[contextKeyActual] = ec.Actual
	}

	for k, v := range ec.Extra {
		result[k] = v
	}

	return result
}
