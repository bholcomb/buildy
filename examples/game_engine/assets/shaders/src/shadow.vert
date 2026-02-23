#version 450

layout(location = 0) in vec3 inPosition;

layout(set = 0, binding = 0) uniform LightSpaceUBO {
    mat4 lightSpaceMatrix;
} lightSpace;

layout(push_constant) uniform PushConstants {
    mat4 model;
} push;

void main() {
    gl_Position = lightSpace.lightSpaceMatrix * push.model * vec4(inPosition, 1.0);
}
