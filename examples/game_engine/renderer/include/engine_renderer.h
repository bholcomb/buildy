#pragma once

#include "renderer/types.h"
#include "renderer/buffer.h"
#include "renderer/texture.h"
#include "renderer/shader.h"
#include "renderer/pipeline.h"
#include "renderer/render_pass.h"
#include "renderer/command_buffer.h"
#include "renderer/mesh.h"
#include "renderer/material.h"
#include "renderer/camera.h"

namespace engine {
namespace renderer {

struct RendererConfig {
    GraphicsAPI api = GraphicsAPI::Vulkan;
    u32 maxFramesInFlight = 2;
    bool validation = true;
    bool vsync = true;
};

class Renderer {
public:
    static Renderer& Instance();
    Result Initialize(const RendererConfig& config, void* windowHandle);
    void Shutdown();
    void BeginFrame();
    void EndFrame();
    void Present();
    CommandBuffer* GetCurrentCommandBuffer();
    u32 GetSwapchainWidth() const { return m_width; }
    u32 GetSwapchainHeight() const { return m_height; }
    void WaitIdle();
private:
    RendererConfig m_config;
    u32 m_width = 1920, m_height = 1080;
    bool m_initialized = false;
};

} // namespace renderer
} // namespace engine
