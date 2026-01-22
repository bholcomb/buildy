#version 450

layout(location = 0) in vec2 fragTexCoord;

layout(location = 0) out vec4 outColor;

layout(set = 0, binding = 0) uniform sampler2D sceneTexture;

layout(push_constant) uniform PostProcessParams {
    float exposure;
    float gamma;
    float saturation;
    float vignette;
} params;

vec3 adjustSaturation(vec3 color, float saturation) {
    float luminance = dot(color, vec3(0.2126, 0.7152, 0.0722));
    return mix(vec3(luminance), color, saturation);
}

float calculateVignette(vec2 uv, float intensity) {
    vec2 center = uv - 0.5;
    float dist = length(center);
    return 1.0 - smoothstep(0.4, 0.8, dist) * intensity;
}

void main() {
    vec3 color = texture(sceneTexture, fragTexCoord).rgb;
    
    // Exposure
    color *= params.exposure;
    
    // Reinhard tonemapping
    color = color / (color + vec3(1.0));
    
    // Saturation adjustment
    color = adjustSaturation(color, params.saturation);
    
    // Vignette
    float vignette = calculateVignette(fragTexCoord, params.vignette);
    color *= vignette;
    
    // Gamma correction
    color = pow(color, vec3(1.0 / params.gamma));
    
    outColor = vec4(color, 1.0);
}
