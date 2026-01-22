#pragma once
#include "renderer/types.h"

namespace engine {
namespace renderer {

struct ShaderHandle { u32 id; bool IsValid() const { return id != 0; } };
struct ShaderProgramHandle { u32 id; bool IsValid() const { return id != 0; } };

ShaderHandle CreateShader(ShaderStage stage, const u8* code, usize codeSize);
void DestroyShader(ShaderHandle shader);
ShaderHandle LoadShaderFromFile(const char* path, ShaderStage stage);
ShaderProgramHandle CreateShaderProgram(ShaderHandle vertex, ShaderHandle fragment);
void DestroyShaderProgram(ShaderProgramHandle program);

} // namespace renderer
} // namespace engine
