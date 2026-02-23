// Command buffer - stub implementation
#include "renderer/command_buffer.h"

namespace engine {
namespace renderer {

class CommandBufferImpl : public CommandBuffer {
public:
    void Begin() override { /* stub */ }
    void End() override { /* stub */ }
    void BeginRenderPass(const RenderPassDesc&) override { /* stub */ }
    void EndRenderPass() override { /* stub */ }
    void SetViewport(const Viewport&) override { /* stub */ }
    void SetScissor(const ScissorRect&) override { /* stub */ }
    void BindPipeline(PipelineHandle) override { /* stub */ }
    void BindVertexBuffer(BufferHandle) override { /* stub */ }
    void BindIndexBuffer(BufferHandle) override { /* stub */ }
    void Draw(u32, u32) override { /* stub */ }
    void DrawIndexed(u32, u32) override { /* stub */ }
};

CommandBuffer* CreateCommandBuffer() { return new CommandBufferImpl(); }
void DestroyCommandBuffer(CommandBuffer* cb) { delete cb; }
void SubmitCommandBuffer(CommandBuffer*) { /* stub */ }

} // namespace renderer
} // namespace engine
