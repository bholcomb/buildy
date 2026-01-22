#pragma once

#include "audio/types.h"
#include "audio/buffer.h"
#include "audio/source.h"
#include "audio/mixer.h"
#include "audio/effects.h"

namespace engine {
namespace audio {

struct AudioConfig {
    u32 sampleRate = 44100;
    u32 bufferSize = 1024;
    u32 maxSources = 32;
};

class AudioSystem {
public:
    static AudioSystem& Instance();
    Result Initialize(const AudioConfig& config);
    void Shutdown();
    void Update(f32 deltaTime);
    void SetMasterVolume(f32 volume);
    f32 GetMasterVolume() const { return m_masterVolume; }
private:
    AudioConfig m_config;
    f32 m_masterVolume = 1.0f;
    bool m_initialized = false;
};

} // namespace audio
} // namespace engine
