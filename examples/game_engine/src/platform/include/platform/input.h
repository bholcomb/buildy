#pragma once
#include "core/types.h"

namespace engine {
namespace platform {

enum class KeyCode : u16 { A, B, C, Space, Escape, Enter, Count };
enum class MouseButton : u8 { Left, Right, Middle, Count };

void InitInput();
void ShutdownInput();
void UpdateInput();
bool IsKeyDown(KeyCode key);
bool IsKeyPressed(KeyCode key);
bool IsMouseButtonDown(MouseButton button);
void GetMousePosition(i32* x, i32* y);
void GetMouseDelta(i32* dx, i32* dy);

} // namespace platform
} // namespace engine
