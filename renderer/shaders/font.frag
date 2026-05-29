#version 330 core

uniform sampler2D u_fontAtlas;

in vec2 v_uv;
in vec4 v_color;
in vec4 v_backgroundColor;

out vec4 fragColor;

void main() {
    float textAlpha = texture(u_fontAtlas, v_uv).r;
    fragColor = mix(v_backgroundColor, vec4(v_color.rgb, textAlpha), textAlpha);
}
