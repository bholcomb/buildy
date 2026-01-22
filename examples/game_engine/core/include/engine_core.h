#pragma once

// Core engine headers - include this for full core functionality
#include "core/types.h"
#include "core/assert.h"
#include "core/logging.h"
#include "core/math.h"
#include "core/memory.h"

namespace engine {

// Engine configuration
struct EngineConfig {
    const char* appName = "Engine Application";
    u32 windowWidth = 1280;
    u32 windowHeight = 720;
    bool fullscreen = false;
    bool vsync = true;
    LogLevel logLevel = LogLevel::Info;
};

// Main engine class
class Engine {
public:
    static Engine& Instance();
    
    Result Initialize(const EngineConfig& config);
    void Shutdown();
    
    void Run();
    void RequestQuit() { m_running = false; }
    bool IsRunning() const { return m_running; }
    
    f64 GetTime() const;
    f64 GetDeltaTime() const { return m_deltaTime; }
    u64 GetFrameCount() const { return m_frameCount; }
    
    const EngineConfig& GetConfig() const { return m_config; }
    
public:
    Engine() = default;
    ~Engine() = default;

private:
    
    EngineConfig m_config;
    bool m_initialized = false;
    bool m_running = false;
    f64 m_deltaTime = 0.0;
    u64 m_frameCount = 0;
};

// Convenience function
inline Engine& GetEngine() { return Engine::Instance(); }

} // namespace engine
