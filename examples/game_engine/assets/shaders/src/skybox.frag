#version 450

layout(location = 0) in vec3 fragTexCoord;

layout(location = 0) out vec4 outColor;

layout(set = 1, binding = 0) uniform samplerCube skyboxTexture;

void main() {
    vec3 color = texture(skyboxTexture, fragTexCoord).rgb;
    
    // Simple tonemapping
    color = color / (color + vec3(1.0));
    
    // Gamma correction
    color = pow(color, vec3(1.0 / 2.2));
    
    outColor = vec4(color, 1.0);
}
