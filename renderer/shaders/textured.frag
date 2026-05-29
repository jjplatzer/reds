#version 330 core

uniform sampler2D u_texture;
uniform vec4 u_color;

in vec2 v_uv;

out vec4 fragColor;

void main() {
    fragColor = texture(u_texture, v_uv) * u_color;
}
