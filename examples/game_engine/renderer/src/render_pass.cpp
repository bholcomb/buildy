// Render pass - stub implementation
#include "renderer/render_pass.h"

namespace engine {
namespace renderer {

static u32 s_nextRenderPassId = 1;

RenderPassHandle CreateRenderPass(const RenderPassDesc&) { return {s_nextRenderPassId++}; }
void DestroyRenderPass(RenderPassHandle) { /* stub */ }

} // namespace renderer
} // namespace engine
