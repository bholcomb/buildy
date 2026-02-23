// Window Linux - stub implementation
#include "platform/window.h"
#include "core/logging.h"

namespace engine {
namespace platform {

static WindowHandle s_window = {(void*)1};

WindowHandle CreateWindow(const WindowConfig& config) {
    LOG_INFO(LogCategory::Platform, "Creating window: %s (%ux%u)", config.title, config.width, config.height);
    return s_window;
}

void DestroyWindow(WindowHandle) { /* stub */ }
void SetWindowTitle(WindowHandle, const char*) { /* stub */ }
void SetWindowSize(WindowHandle, u32, u32) { /* stub */ }
void GetWindowSize(WindowHandle, u32* w, u32* h) { *w = 1920; *h = 1080; }
void* GetNativeWindowHandle(WindowHandle h) { return h.native; }

} // namespace platform
} // namespace engine
