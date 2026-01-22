#pragma once
#include "audio/types.h"

namespace engine {
namespace audio {

struct AudioBufferHandle { u32 id; bool IsValid() const { return id != 0; } };

struct AudioBufferDesc {
    AudioFormat format;
    u32 sampleRate;
    usize dataSize;
    const void* data;
};

AudioBufferHandle CreateAudioBuffer(const AudioBufferDesc& desc);
void DestroyAudioBuffer(AudioBufferHandle buffer);
AudioBufferHandle LoadAudioFromFile(const char* path);
AudioBufferHandle LoadAudioFromMemory(const u8* data, usize size);

} // namespace audio
} // namespace engine
