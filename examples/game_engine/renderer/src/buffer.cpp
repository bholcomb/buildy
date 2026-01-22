// Buffer - stub implementation
#include "renderer/buffer.h"

namespace engine {
namespace renderer {

static u32 s_nextBufferId = 1;

BufferHandle CreateBuffer(const BufferDesc&) { return {s_nextBufferId++}; }
void DestroyBuffer(BufferHandle) { /* stub */ }
void UpdateBuffer(BufferHandle, const void*, usize, usize) { /* stub */ }
void* MapBuffer(BufferHandle) { return nullptr; }
void UnmapBuffer(BufferHandle) { /* stub */ }

} // namespace renderer
} // namespace engine
