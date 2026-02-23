// Texture - stub implementation
#include "renderer/texture.h"

namespace engine {
namespace renderer {

static u32 s_nextTextureId = 1;
static u32 s_nextSamplerId = 1;

TextureHandle CreateTexture(const TextureDesc&) { return {s_nextTextureId++}; }
void DestroyTexture(TextureHandle) { /* stub */ }
void UpdateTexture(TextureHandle, const void*, usize) { /* stub */ }
SamplerHandle CreateSampler() { return {s_nextSamplerId++}; }
void DestroySampler(SamplerHandle) { /* stub */ }

} // namespace renderer
} // namespace engine
