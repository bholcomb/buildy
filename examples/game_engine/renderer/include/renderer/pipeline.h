#pragma once
#include "renderer/types.h"
#include "renderer/shader.h"

namespace engine {
namespace renderer {

struct PipelineHandle { u32 id; bool IsValid() const { return id != 0; } };

struct PipelineDesc {
    ShaderProgramHandle shaderProgram;
    CullMode cullMode;
    FillMode fillMode;
    bool depthTest;
    bool depthWrite;
    bool blendEnabled;
};

PipelineHandle CreatePipeline(const PipelineDesc& desc);
void DestroyPipeline(PipelineHandle pipeline);

} // namespace renderer
} // namespace engine
