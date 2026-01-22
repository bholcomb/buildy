#version 450

// Input from vertex shader
layout(location = 0) in vec3 fragPosition;
layout(location = 1) in vec3 fragNormal;
layout(location = 2) in vec2 fragTexCoord;
layout(location = 3) in vec3 fragTangent;
layout(location = 4) in vec3 fragBitangent;

// Output color
layout(location = 0) out vec4 outColor;

// Camera UBO
layout(set = 0, binding = 0) uniform CameraUBO {
    mat4 view;
    mat4 projection;
    mat4 viewProjection;
    vec4 cameraPosition;
    vec4 cameraDirection;
} camera;

// Material UBO
layout(set = 1, binding = 0) uniform MaterialUBO {
    vec4 baseColor;
    float metallic;
    float roughness;
    float ao;
    float emissiveStrength;
    vec3 emissiveColor;
    float _padding;
} material;

// Textures
layout(set = 1, binding = 1) uniform sampler2D albedoMap;
layout(set = 1, binding = 2) uniform sampler2D normalMap;
layout(set = 1, binding = 3) uniform sampler2D metallicRoughnessMap;
layout(set = 1, binding = 4) uniform sampler2D aoMap;
layout(set = 1, binding = 5) uniform sampler2D emissiveMap;

// Light data
layout(set = 2, binding = 0) uniform LightUBO {
    vec4 lightPositions[4];
    vec4 lightColors[4];
    int lightCount;
} lights;

const float PI = 3.14159265359;

// Normal Distribution Function (GGX/Trowbridge-Reitz)
float DistributionGGX(vec3 N, vec3 H, float roughness) {
    float a = roughness * roughness;
    float a2 = a * a;
    float NdotH = max(dot(N, H), 0.0);
    float NdotH2 = NdotH * NdotH;
    
    float num = a2;
    float denom = (NdotH2 * (a2 - 1.0) + 1.0);
    denom = PI * denom * denom;
    
    return num / denom;
}

// Geometry Function (Schlick-GGX)
float GeometrySchlickGGX(float NdotV, float roughness) {
    float r = (roughness + 1.0);
    float k = (r * r) / 8.0;
    
    float num = NdotV;
    float denom = NdotV * (1.0 - k) + k;
    
    return num / denom;
}

float GeometrySmith(vec3 N, vec3 V, vec3 L, float roughness) {
    float NdotV = max(dot(N, V), 0.0);
    float NdotL = max(dot(N, L), 0.0);
    float ggx2 = GeometrySchlickGGX(NdotV, roughness);
    float ggx1 = GeometrySchlickGGX(NdotL, roughness);
    
    return ggx1 * ggx2;
}

// Fresnel (Schlick approximation)
vec3 FresnelSchlick(float cosTheta, vec3 F0) {
    return F0 + (1.0 - F0) * pow(clamp(1.0 - cosTheta, 0.0, 1.0), 5.0);
}

void main() {
    // Sample textures
    vec4 albedo = texture(albedoMap, fragTexCoord) * material.baseColor;
    vec2 metallicRoughness = texture(metallicRoughnessMap, fragTexCoord).bg;
    float metallic = metallicRoughness.x * material.metallic;
    float roughness = metallicRoughness.y * material.roughness;
    float ao = texture(aoMap, fragTexCoord).r * material.ao;
    
    // Normal mapping
    vec3 tangentNormal = texture(normalMap, fragTexCoord).xyz * 2.0 - 1.0;
    mat3 TBN = mat3(fragTangent, fragBitangent, fragNormal);
    vec3 N = normalize(TBN * tangentNormal);
    
    // View direction
    vec3 V = normalize(camera.cameraPosition.xyz - fragPosition);
    
    // Base reflectivity
    vec3 F0 = vec3(0.04);
    F0 = mix(F0, albedo.rgb, metallic);
    
    // Accumulate lighting
    vec3 Lo = vec3(0.0);
    
    for (int i = 0; i < lights.lightCount; i++) {
        vec3 L = normalize(lights.lightPositions[i].xyz - fragPosition);
        vec3 H = normalize(V + L);
        
        float distance = length(lights.lightPositions[i].xyz - fragPosition);
        float attenuation = 1.0 / (distance * distance);
        vec3 radiance = lights.lightColors[i].rgb * attenuation;
        
        // Cook-Torrance BRDF
        float NDF = DistributionGGX(N, H, roughness);
        float G = GeometrySmith(N, V, L, roughness);
        vec3 F = FresnelSchlick(max(dot(H, V), 0.0), F0);
        
        vec3 kS = F;
        vec3 kD = vec3(1.0) - kS;
        kD *= 1.0 - metallic;
        
        vec3 numerator = NDF * G * F;
        float denominator = 4.0 * max(dot(N, V), 0.0) * max(dot(N, L), 0.0) + 0.0001;
        vec3 specular = numerator / denominator;
        
        float NdotL = max(dot(N, L), 0.0);
        Lo += (kD * albedo.rgb / PI + specular) * radiance * NdotL;
    }
    
    // Ambient
    vec3 ambient = vec3(0.03) * albedo.rgb * ao;
    
    // Emissive
    vec3 emissive = texture(emissiveMap, fragTexCoord).rgb * material.emissiveColor * material.emissiveStrength;
    
    // Final color
    vec3 color = ambient + Lo + emissive;
    
    // HDR tonemapping
    color = color / (color + vec3(1.0));
    
    // Gamma correction
    color = pow(color, vec3(1.0 / 2.2));
    
    outColor = vec4(color, albedo.a);
}
