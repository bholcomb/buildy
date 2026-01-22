// Audio system - stub implementation
#include "engine_audio.h"
#include "core/logging.h"

namespace engine {
namespace audio {

static AudioSystem s_audioSystem;

AudioSystem& AudioSystem::Instance() { return s_audioSystem; }

Result AudioSystem::Initialize(const AudioConfig& config) {
    m_config = config;
    LOG_INFO(LogCategory::Audio, "Initializing audio system...");
    LOG_INFO(LogCategory::Audio, "Sample rate: %u Hz", config.sampleRate);
    LOG_INFO(LogCategory::Audio, "Buffer size: %u samples", config.bufferSize);
    LOG_INFO(LogCategory::Audio, "Max sources: %u", config.maxSources);
    LOG_INFO(LogCategory::Audio, "Audio system initialized successfully");
    m_initialized = true;
    return Result::Success;
}

void AudioSystem::Shutdown() { 
    LOG_INFO(LogCategory::Audio, "Audio system shutdown"); 
    m_initialized = false;
}
void AudioSystem::Update(f32) { /* stub */ }
void AudioSystem::SetMasterVolume(f32 vol) { m_masterVolume = vol; }

} // namespace audio
} // namespace engine
