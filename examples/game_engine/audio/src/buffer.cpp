// Audio buffer - stub implementation
#include "audio/buffer.h"

namespace engine {
namespace audio {

static u32 s_nextBufferId = 1;

AudioBufferHandle CreateAudioBuffer(const AudioBufferDesc&) { return {s_nextBufferId++}; }
void DestroyAudioBuffer(AudioBufferHandle) { /* stub */ }
AudioBufferHandle LoadAudioFromFile(const char*) { return {s_nextBufferId++}; }
AudioBufferHandle LoadAudioFromMemory(const u8*, usize) { return {s_nextBufferId++}; }

} // namespace audio
} // namespace engine
