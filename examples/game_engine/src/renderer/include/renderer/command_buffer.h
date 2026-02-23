#pragma once
#include "renderer/types.h"
#include "renderer/buffer.h"
#include "renderer/texture.h"
#include "renderer/pipeline.h"
#include "renderer/render_pass.h"

namespace engine {
namespace renderer {

class CommandBuffer {
public:
    virtual ~CommandBuffer() = default;
    virtual void Begin() = 0;
    virtual void End() = 0;
    virtual void BeginRenderPass(const RenderPassDesc& desc) = 0;
    virtual void EndRenderPass() = 0;
    virtual void SetViewport(const Viewport& viewport) = 0;
    virtual void SetScissor(const ScissorRect& scissor) = 0;
    virtual void BindPipeline(PipelineHandle pipeline) = 0;
    virtual void BindVertexBuffer(BufferHandle buffer) = 0;
    virtual void BindIndexBuffer(BufferHandle buffer) = 0;
    virtual void Draw(u32 vertexCount, u32 firstVertex) = 0;
    virtual void DrawIndexed(u32 indexCount, u32 firstIndex) = 0;
};

CommandBuffer* CreateCommandBuffer();
void DestroyCommandBuffer(CommandBuffer* cmdBuffer);
void SubmitCommandBuffer(CommandBuffer* cmdBuffer);

} // namespace renderer
} // namespace engine
