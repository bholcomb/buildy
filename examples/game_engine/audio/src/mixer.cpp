// Audio mixer - stub implementation
#include "audio/mixer.h"
#include "core/logging.h"

namespace engine {
namespace audio {

static AudioMixer s_mixer;

AudioMixer& AudioMixer::Instance() { return s_mixer; }

Result AudioMixer::Initialize(const MixerConfig& config) {
    m_config = config;
    LOG_DEBUG(LogCategory::Audio, "Created audio bus: Master (index 0)");
    LOG_INFO(LogCategory::Audio, "Audio mixer initialized");
    LOG_INFO(LogCategory::Audio, "Max voices: %u", config.maxVoices);
    m_initialized = true;
    return Result::Success;
}

void AudioMixer::Shutdown() { m_initialized = false; }
void AudioMixer::Update(f32) { /* stub */ }
void AudioMixer::SetMasterVolume(f32 vol) { m_masterVolume = vol; }
u32 AudioMixer::CreateBus(const char*) { return 0; }
void AudioMixer::SetBusVolume(u32, f32) { /* stub */ }

} // namespace audio
} // namespace engine
