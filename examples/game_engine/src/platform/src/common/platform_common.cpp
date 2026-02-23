// Platform common - stub implementation
#include "platform/platform.h"

namespace engine {
namespace platform {

static bool s_initialized = false;

Result InitPlatform() {
    if (s_initialized) return Result::Success;
    Result r = InitPlatformNative();
    if (r == Result::Success) s_initialized = true;
    return r;
}

void ShutdownPlatform() {
    if (!s_initialized) return;
    ShutdownPlatformNative();
    s_initialized = false;
}

PlatformInfo GetPlatformInfo() { return GetPlatformInfoNative(); }

} // namespace platform
} // namespace engine
