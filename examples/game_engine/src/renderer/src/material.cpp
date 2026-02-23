// Material - stub implementation
#include "renderer/material.h"
#include "renderer/command_buffer.h"

namespace engine {
namespace renderer {

Material::Material() : m_program({0}), m_pipeline({0}), m_textures() {}
Material::~Material() { /* stub */ }
void Material::Create(ShaderProgramHandle program) { m_program = program; }
void Material::Destroy() { m_program = {0}; m_pipeline = {0}; }
void Material::SetTexture(u32 slot, TextureHandle tex) { if (slot < 8) m_textures[slot] = tex; }
void Material::Bind(CommandBuffer*) const { /* stub */ }

} // namespace renderer
} // namespace engine
