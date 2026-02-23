#pragma once
#include "audio/types.h"
#include "audio/buffer.h"
#include "core/math.h"

namespace engine {
namespace audio {

struct AudioSourceHandle { u32 id; bool IsValid() const { return id != 0; } };

AudioSourceHandle CreateAudioSource();
void DestroyAudioSource(AudioSourceHandle source);
void PlaySource(AudioSourceHandle source);
void PauseSource(AudioSourceHandle source);
void StopSource(AudioSourceHandle source);
bool IsSourcePlaying(AudioSourceHandle source);
void SetSourceBuffer(AudioSourceHandle source, AudioBufferHandle buffer);
void SetSourceVolume(AudioSourceHandle source, f32 volume);
void SetSourcePitch(AudioSourceHandle source, f32 pitch);
void SetSourceLooping(AudioSourceHandle source, bool looping);
void SetSourcePosition(AudioSourceHandle source, const math::Vec3& position);

} // namespace audio
} // namespace engine
