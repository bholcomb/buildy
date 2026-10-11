package workspace

import "buildy/internal/config"

// BuildContext is what is being built: platform, architecture, configuration and, for filters,
// the toolchain. Filtered lists are resolved against it.
type BuildContext = config.Context
