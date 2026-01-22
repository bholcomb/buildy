// Audio source - stub implementation
#include "audio/source.h"

namespace engine {
namespace audio {

static u32 s_nextSourceId = 1;

AudioSourceHandle CreateAudioSource() { return {s_nextSourceId++}; }
void DestroyAudioSource(AudioSourceHandle) { /* stub */ }
void PlaySource(AudioSourceHandle) { /* stub */ }
void PauseSource(AudioSourceHandle) { /* stub */ }
void StopSource(AudioSourceHandle) { /* stub */ }
bool IsSourcePlaying(AudioSourceHandle) { return false; }
void SetSourceBuffer(AudioSourceHandle, AudioBufferHandle) { /* stub */ }
void SetSourceVolume(AudioSourceHandle, f32) { /* stub */ }
void SetSourcePitch(AudioSourceHandle, f32) { /* stub */ }
void SetSourceLooping(AudioSourceHandle, bool) { /* stub */ }
void SetSourcePosition(AudioSourceHandle, const math::Vec3&) { /* stub */ }

} // namespace audio
} // namespace engine
