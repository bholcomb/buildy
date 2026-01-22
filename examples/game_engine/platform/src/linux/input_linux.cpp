// Input Linux - stub implementation
#include "platform/input.h"
#include "core/logging.h"

namespace engine {
namespace platform {

void InitInput() { LOG_INFO(LogCategory::Input, "Initializing input system (Linux)"); }
void ShutdownInput() { /* stub */ }
void UpdateInput() { /* stub */ }
bool IsKeyDown(KeyCode) { return false; }
bool IsKeyPressed(KeyCode) { return false; }
bool IsMouseButtonDown(MouseButton) { return false; }
void GetMousePosition(i32* x, i32* y) { *x = 0; *y = 0; }
void GetMouseDelta(i32* dx, i32* dy) { *dx = 0; *dy = 0; }

} // namespace platform
} // namespace engine
