#pragma once
#include "renderer/texture.h"
#include "renderer/shader.h"
#include "renderer/pipeline.h"

namespace engine {
namespace renderer {

class CommandBuffer;

class Material {
public:
    Material();
    ~Material();
    void Create(ShaderProgramHandle program);
    void Destroy();
    void SetTexture(u32 slot, TextureHandle texture);
    void Bind(CommandBuffer* cmdBuffer) const;
    PipelineHandle GetPipeline() const { return m_pipeline; }
private:
    ShaderProgramHandle m_program;
    PipelineHandle m_pipeline;
    TextureHandle m_textures[8];
};

} // namespace renderer
} // namespace engine
