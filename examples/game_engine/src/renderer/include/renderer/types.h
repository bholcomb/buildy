#pragma once
#include "core/types.h"

namespace engine {
namespace renderer {

enum class GraphicsAPI : u8 { Vulkan, OpenGL, D3D12, Metal };
enum class TextureFormat : u8 { RGBA8, RGBA16F, RGBA32F, Depth24, Depth32F };
enum class BufferUsage : u8 { Vertex, Index, Uniform, Storage };
enum class ShaderStage : u8 { Vertex, Fragment, Compute };
enum class CullMode : u8 { None, Front, Back };
enum class FillMode : u8 { Solid, Wireframe };

struct Viewport { f32 x, y, width, height, minDepth, maxDepth; };
struct ScissorRect { i32 x, y; u32 width, height; };
struct ClearColor { f32 r, g, b, a; };

} // namespace renderer
} // namespace engine
