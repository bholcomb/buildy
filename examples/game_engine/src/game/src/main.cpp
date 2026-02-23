// Main - stub implementation
#include "engine_core.h"
#include "engine_platform.h"
#include "engine_renderer.h"
#include "engine_audio.h"
#include "core/logging.h"

using namespace engine;

int main() {
    // Initialize core engine
    EngineConfig engineConfig = {};
    engineConfig.appName = "Game Engine Demo";
    engineConfig.windowWidth = 1920;
    engineConfig.windowHeight = 1080;
    Engine::Instance().Initialize(engineConfig);

    // Initialize platform
    platform::InitPlatform();
    auto info = platform::GetPlatformInfo();
    LOG_INFO(LogCategory::Platform, "Platform: %s", info.name);
    LOG_INFO(LogCategory::Platform, "Processors: %u, Memory: %llu MB", 
             info.processorCount, info.totalMemory / (1024*1024));

    // Create window
    platform::WindowConfig windowConfig = {};
    windowConfig.title = "Game Engine Demo";
    windowConfig.width = 1920;
    windowConfig.height = 1080;
    auto window = platform::CreateWindow(windowConfig);
    platform::InitInput();

    // Initialize renderer (with window handle)
    renderer::RendererConfig rendererConfig = {};
    rendererConfig.maxFramesInFlight = 2;
    renderer::Renderer::Instance().Initialize(rendererConfig, platform::GetNativeWindowHandle(window));

    // Initialize audio
    audio::AudioConfig audioConfig = {};
    audioConfig.sampleRate = 44100;
    audioConfig.bufferSize = 1024;
    audioConfig.maxSources = 32;
    audio::AudioSystem::Instance().Initialize(audioConfig);
    
    audio::MixerConfig mixerConfig = {};
    mixerConfig.maxVoices = 32;
    audio::AudioMixer::Instance().Initialize(mixerConfig);

    LOG_INFO(LogCategory::Game, "Game initialized successfully");

    // Shutdown (immediate for stub)
    audio::AudioMixer::Instance().Shutdown();
    audio::AudioSystem::Instance().Shutdown();
    renderer::Renderer::Instance().Shutdown();
    platform::ShutdownInput();
    platform::ShutdownPlatform();
    Engine::Instance().Shutdown();

    return 0;
}
