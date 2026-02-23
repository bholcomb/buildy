// Audio effects - stub implementation
#include "audio/effects.h"

namespace engine {
namespace audio {

static u32 s_nextEffectId = 1;

AudioEffectHandle CreateReverbEffect(const ReverbParams&) { return {s_nextEffectId++}; }
AudioEffectHandle CreateDelayEffect(const DelayParams&) { return {s_nextEffectId++}; }
AudioEffectHandle CreateEchoEffect(const EchoParams&) { return {s_nextEffectId++}; }
void DestroyEffect(AudioEffectHandle) { /* stub */ }
void AttachEffectToBus(u32, AudioEffectHandle) { /* stub */ }
void DetachEffectFromBus(u32, AudioEffectHandle) { /* stub */ }

} // namespace audio
} // namespace engine
