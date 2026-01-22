#pragma once
#include "audio/types.h"
#include "audio/source.h"

namespace engine {
namespace audio {

struct MixerConfig {
    u32 sampleRate = 44100;
    u32 bufferSize = 1024;
    u32 maxVoices = 64;
};

class AudioMixer {
public:
    static AudioMixer& Instance();
    Result Initialize(const MixerConfig& config);
    void Shutdown();
    void Update(f32 deltaTime);
    void SetMasterVolume(f32 volume);
    f32 GetMasterVolume() const { return m_masterVolume; }
    u32 CreateBus(const char* name);
    void SetBusVolume(u32 busIndex, f32 volume);
private:
    MixerConfig m_config;
    f32 m_masterVolume = 1.0f;
    bool m_initialized = false;
};

} // namespace audio
} // namespace engine
