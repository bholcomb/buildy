#pragma once
#include "renderer/buffer.h"

namespace engine {
namespace renderer {

class CommandBuffer;

class Mesh {
public:
    Mesh();
    ~Mesh();
    void Create(const void* vertices, u32 vertexSize, u32 vertexCount, const u16* indices, u32 indexCount);
    void Destroy();
    void Draw(CommandBuffer* cmdBuffer) const;
    BufferHandle GetVertexBuffer() const { return m_vertexBuffer; }
    BufferHandle GetIndexBuffer() const { return m_indexBuffer; }
    u32 GetIndexCount() const { return m_indexCount; }
private:
    BufferHandle m_vertexBuffer;
    BufferHandle m_indexBuffer;
    u32 m_indexCount;
};

} // namespace renderer
} // namespace engine
