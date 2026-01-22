// Pipeline - stub implementation
#include "renderer/pipeline.h"

namespace engine {
namespace renderer {

static u32 s_nextPipelineId = 1;

PipelineHandle CreatePipeline(const PipelineDesc&) { return {s_nextPipelineId++}; }
void DestroyPipeline(PipelineHandle) { /* stub */ }

} // namespace renderer
} // namespace engine
