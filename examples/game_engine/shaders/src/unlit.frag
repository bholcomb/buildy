#version 450

layout(location = 0) in vec2 fragTexCoord;

layout(location = 0) out vec4 outColor;

layout(set = 1, binding = 0) uniform MaterialUBO {
    vec4 color;
} material;

layout(set = 1, binding = 1) uniform sampler2D colorTexture;

void main() {
    vec4 texColor = texture(colorTexture, fragTexCoord);
    outColor = texColor * material.color;
}
