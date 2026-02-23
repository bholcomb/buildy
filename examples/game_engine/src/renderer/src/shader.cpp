// Shader - stub implementation
#include "renderer/shader.h"

namespace engine {
namespace renderer {

static u32 s_nextShaderId = 1;
static u32 s_nextProgramId = 1;

ShaderHandle CreateShader(ShaderStage, const u8*, usize) { return {s_nextShaderId++}; }
void DestroyShader(ShaderHandle) { /* stub */ }
ShaderHandle LoadShaderFromFile(const char*, ShaderStage) { return {s_nextShaderId++}; }
ShaderProgramHandle CreateShaderProgram(ShaderHandle, ShaderHandle) { return {s_nextProgramId++}; }
void DestroyShaderProgram(ShaderProgramHandle) { /* stub */ }

} // namespace renderer
} // namespace engine
