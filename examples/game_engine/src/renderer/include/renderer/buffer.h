#pragma once
#include "renderer/types.h"

namespace engine {
namespace renderer {

struct BufferHandle { u32 id; bool IsValid() const { return id != 0; } };

struct BufferDesc {
    BufferUsage usage;
    usize size;
    const void* initialData;
};

BufferHandle CreateBuffer(const BufferDesc& desc);
void DestroyBuffer(BufferHandle buffer);
void UpdateBuffer(BufferHandle buffer, const void* data, usize size, usize offset);
void* MapBuffer(BufferHandle buffer);
void UnmapBuffer(BufferHandle buffer);

} // namespace renderer
} // namespace engine
