#pragma once
#include "renderer/types.h"
#include "renderer/texture.h"

namespace engine {
namespace renderer {

struct RenderPassHandle { u32 id; bool IsValid() const { return id != 0; } };

struct RenderPassDesc {
    TextureHandle colorAttachment;
    TextureHandle depthAttachment;
    ClearColor clearColor;
    f32 clearDepth;
    bool loadColor;
    bool storeColor;
};

RenderPassHandle CreateRenderPass(const RenderPassDesc& desc);
void DestroyRenderPass(RenderPassHandle pass);

} // namespace renderer
} // namespace engine
