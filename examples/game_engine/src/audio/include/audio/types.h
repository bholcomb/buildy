#pragma once
#include "core/types.h"

namespace engine {
namespace audio {

enum class AudioFormat : u8 { Mono8, Mono16, Stereo8, Stereo16, MonoF32, StereoF32 };
enum class AudioState : u8 { Stopped, Playing, Paused };

} // namespace audio
} // namespace engine
