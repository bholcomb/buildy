#include "platform/window.h"
#include "core/logging.h"

#ifdef _WIN32

// Stub implementation for Windows

namespace engine {
namespace platform {

struct WindowsWindow {
    WindowConfig config;
    bool visible;
    bool focused;
    bool minimized;
    i32 x, y;
};

WindowHandle CreateWindow(const WindowConfig& config) {
    LOG_INFO(LogCategory::Platform, "Creating window: %s (%dx%d)",
             config.title, config.width, config.height);
    
    WindowsWindow* window = new WindowsWindow();
    window->config = config;
    window->visible = true;
    window->focused = true;
    window->minimized = false;
    window->x = 100;
    window->y = 100;
    
    return {window};
}

void DestroyWindow(WindowHandle handle) {
    if (handle.IsValid()) {
        delete static_cast<WindowsWindow*>(handle.native);
    }
}

void SetWindowTitle(WindowHandle handle, const char* title) {
    if (handle.IsValid()) {
        static_cast<WindowsWindow*>(handle.native)->config.title = title;
    }
}

void SetWindowSize(WindowHandle handle, u32 width, u32 height) {
    if (handle.IsValid()) {
        WindowsWindow* w = static_cast<WindowsWindow*>(handle.native);
        w->config.width = width;
        w->config.height = height;
    }
}

void SetWindowPosition(WindowHandle handle, i32 x, i32 y) {
    if (handle.IsValid()) {
        WindowsWindow* w = static_cast<WindowsWindow*>(handle.native);
        w->x = x;
        w->y = y;
    }
}

void SetWindowFullscreen(WindowHandle handle, bool fullscreen) {
    if (handle.IsValid()) {
        static_cast<WindowsWindow*>(handle.native)->config.fullscreen = fullscreen;
    }
}

void SetWindowVisible(WindowHandle handle, bool visible) {
    if (handle.IsValid()) {
        static_cast<WindowsWindow*>(handle.native)->visible = visible;
    }
}

math::Vec2 GetWindowSize(WindowHandle handle) {
    if (handle.IsValid()) {
        WindowsWindow* w = static_cast<WindowsWindow*>(handle.native);
        return math::Vec2(static_cast<f32>(w->config.width), 
                          static_cast<f32>(w->config.height));
    }
    return math::Vec2();
}

math::Vec2 GetWindowPosition(WindowHandle handle) {
    if (handle.IsValid()) {
        WindowsWindow* w = static_cast<WindowsWindow*>(handle.native);
        return math::Vec2(static_cast<f32>(w->x), static_cast<f32>(w->y));
    }
    return math::Vec2();
}

bool IsWindowFullscreen(WindowHandle handle) {
    return handle.IsValid() && static_cast<WindowsWindow*>(handle.native)->config.fullscreen;
}

bool IsWindowFocused(WindowHandle handle) {
    return handle.IsValid() && static_cast<WindowsWindow*>(handle.native)->focused;
}

bool IsWindowMinimized(WindowHandle handle) {
    return handle.IsValid() && static_cast<WindowsWindow*>(handle.native)->minimized;
}

bool PollWindowEvent(WindowHandle, WindowEvent* event) {
    event->type = WindowEventType::None;
    return false;
}

void* GetNativeWindowHandle(WindowHandle handle) {
    return handle.native;
}

void* GetNativeDisplayHandle() {
    return nullptr;
}

} // namespace platform
} // namespace engine

#endif // _WIN32
