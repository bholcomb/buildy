#include "platform/input.h"
#include "core/logging.h"
#include <cstring>

#ifdef _WIN32

namespace engine {
namespace platform {

static KeyboardState s_keyboardState;
static MouseState s_mouseState;
static GamepadState s_gamepadStates[4];

void InitInput() {
    LOG_INFO(LogCategory::Input, "Initializing input system (Windows)");
    
    std::memset(&s_keyboardState, 0, sizeof(s_keyboardState));
    std::memset(&s_mouseState, 0, sizeof(s_mouseState));
    std::memset(s_gamepadStates, 0, sizeof(s_gamepadStates));
}

void ShutdownInput() {
    LOG_INFO(LogCategory::Input, "Shutting down input system");
}

void UpdateInput() {
    std::memcpy(s_keyboardState.previousKeys, s_keyboardState.keys, sizeof(s_keyboardState.keys));
    std::memcpy(s_mouseState.previousButtons, s_mouseState.buttons, sizeof(s_mouseState.buttons));
    for (int i = 0; i < 4; i++) {
        std::memcpy(s_gamepadStates[i].previousButtons, s_gamepadStates[i].buttons, 
                    sizeof(s_gamepadStates[i].buttons));
    }
    
    s_mouseState.delta = math::Vec2(0, 0);
    s_mouseState.scrollDelta = 0;
}

bool IsKeyDown(KeyCode key) {
    u16 idx = static_cast<u16>(key);
    return idx < 256 && s_keyboardState.keys[idx];
}

bool IsKeyPressed(KeyCode key) {
    u16 idx = static_cast<u16>(key);
    return idx < 256 && s_keyboardState.keys[idx] && !s_keyboardState.previousKeys[idx];
}

bool IsKeyReleased(KeyCode key) {
    u16 idx = static_cast<u16>(key);
    return idx < 256 && !s_keyboardState.keys[idx] && s_keyboardState.previousKeys[idx];
}

const KeyboardState& GetKeyboardState() {
    return s_keyboardState;
}

math::Vec2 GetMousePosition() {
    return s_mouseState.position;
}

math::Vec2 GetMouseDelta() {
    return s_mouseState.delta;
}

f32 GetScrollDelta() {
    return s_mouseState.scrollDelta;
}

bool IsMouseButtonDown(MouseButton button) {
    u8 idx = static_cast<u8>(button);
    return idx < 5 && s_mouseState.buttons[idx];
}

bool IsMouseButtonPressed(MouseButton button) {
    u8 idx = static_cast<u8>(button);
    return idx < 5 && s_mouseState.buttons[idx] && !s_mouseState.previousButtons[idx];
}

bool IsMouseButtonReleased(MouseButton button) {
    u8 idx = static_cast<u8>(button);
    return idx < 5 && !s_mouseState.buttons[idx] && s_mouseState.previousButtons[idx];
}

void SetMousePosition(math::Vec2 position) {
    s_mouseState.position = position;
}

void SetMouseCursorVisible(bool) {
}

void SetMouseCursorLocked(bool) {
}

const MouseState& GetMouseState() {
    return s_mouseState;
}

bool IsGamepadConnected(u32 index) {
    return index < 4 && s_gamepadStates[index].connected;
}

bool IsGamepadButtonDown(u32 index, GamepadButton button) {
    if (index >= 4) return false;
    u8 idx = static_cast<u8>(button);
    return idx < 16 && s_gamepadStates[index].buttons[idx];
}

bool IsGamepadButtonPressed(u32 index, GamepadButton button) {
    if (index >= 4) return false;
    u8 idx = static_cast<u8>(button);
    return idx < 16 && s_gamepadStates[index].buttons[idx] && 
           !s_gamepadStates[index].previousButtons[idx];
}

f32 GetGamepadAxis(u32 index, GamepadAxis axis) {
    if (index >= 4) return 0.0f;
    u8 idx = static_cast<u8>(axis);
    return idx < 6 ? s_gamepadStates[index].axes[idx] : 0.0f;
}

const GamepadState& GetGamepadState(u32 index) {
    static GamepadState empty = {};
    return index < 4 ? s_gamepadStates[index] : empty;
}

} // namespace platform
} // namespace engine

#endif // _WIN32
