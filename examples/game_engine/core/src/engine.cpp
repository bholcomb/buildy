// Core engine - stub implementation
#include "engine_core.h"

namespace engine {

static Engine s_engine;

Engine& Engine::Instance() { return s_engine; }
Result Engine::Initialize(const EngineConfig& config) {
    LOG_INFO(LogCategory::Core, "Engine initializing: %s", config.appName);
    LOG_INFO(LogCategory::Core, "Window size: %ux%u", config.windowWidth, config.windowHeight);
    LOG_INFO(LogCategory::Core, "Engine initialized successfully");
    return Result::Success;
}
void Engine::Shutdown() { LOG_INFO(LogCategory::Core, "Engine shutdown"); }
void Engine::Run() { /* Main loop stub */ }

} // namespace engine
