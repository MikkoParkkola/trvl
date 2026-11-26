package providers

import "github.com/MikkoParkkola/trvl/internal/netguard"

// The address policy lives in internal/netguard so batchexec, which this
// package imports, can apply the same rules to its proxy dial. These names stay
// here because callers and tests outside the package already use them.
const (
	AllowLocalEnv        = netguard.AllowLocalEnv
	AllowPrivateProxyEnv = netguard.AllowPrivateProxyEnv
)

var ErrDestinationRefused = netguard.ErrDestinationRefused
