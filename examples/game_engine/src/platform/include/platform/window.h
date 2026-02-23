#pragma once
#include "core/types.h"

namespace engine {
namespace platform {

struct WindowConfig {
    const char* title = "Window";
    u32 width = 1280;
    u32 height = 720;
    bool fullscreen = false;
};

struct WindowHandle {
    void* native;
    bool IsValid() const { return native != nullptr; }
};

WindowHandle CreateWindow(const WindowConfig& config);
void DestroyWindow(WindowHandle window);
void SetWindowTitle(WindowHandle window, const char* title);
void SetWindowSize(WindowHandle window, u32 width, u32 height);
void GetWindowSize(WindowHandle window, u32* width, u32* height);
void* GetNativeWindowHandle(WindowHandle window);

} // namespace platform
} // namespace engine
