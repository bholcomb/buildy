#pragma once
#include "renderer/types.h"

namespace engine {
namespace renderer {

struct TextureHandle { u32 id; bool IsValid() const { return id != 0; } };
struct SamplerHandle { u32 id; bool IsValid() const { return id != 0; } };

struct TextureDesc {
    u32 width, height, depth;
    TextureFormat format;
    u32 mipLevels;
    const void* initialData;
};

TextureHandle CreateTexture(const TextureDesc& desc);
void DestroyTexture(TextureHandle texture);
void UpdateTexture(TextureHandle texture, const void* data, usize size);
SamplerHandle CreateSampler();
void DestroySampler(SamplerHandle sampler);

} // namespace renderer
} // namespace engine
