// Mesh - stub implementation
#include "renderer/mesh.h"
#include "renderer/command_buffer.h"

namespace engine {
namespace renderer {

Mesh::Mesh() : m_vertexBuffer({0}), m_indexBuffer({0}), m_indexCount(0) {}
Mesh::~Mesh() { /* stub */ }
void Mesh::Create(const void*, u32, u32, const u16*, u32 indexCount) { m_indexCount = indexCount; }
void Mesh::Destroy() { m_indexCount = 0; }
void Mesh::Draw(CommandBuffer*) const { /* stub */ }

} // namespace renderer
} // namespace engine
