#pragma once
#include "audio/types.h"

namespace engine {
namespace audio {

struct AudioEffectHandle { u32 id; bool IsValid() const { return id != 0; } };

struct ReverbParams { f32 roomSize, damping, wetDry; };
struct DelayParams { f32 delayTime, feedback, wetDry; };
struct EchoParams { f32 delay, decay, wetDry; };

AudioEffectHandle CreateReverbEffect(const ReverbParams& params);
AudioEffectHandle CreateDelayEffect(const DelayParams& params);
AudioEffectHandle CreateEchoEffect(const EchoParams& params);
void DestroyEffect(AudioEffectHandle effect);
void AttachEffectToBus(u32 busIndex, AudioEffectHandle effect);
void DetachEffectFromBus(u32 busIndex, AudioEffectHandle effect);

} // namespace audio
} // namespace engine
