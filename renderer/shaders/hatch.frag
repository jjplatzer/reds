#version 330 core

const int yScale = 4;
const int spacing = 50;
const int width = 5;

uniform float u_offset;
uniform vec4 u_color;

out vec4 fragColor;

void main() {
    if (mod(u_offset + gl_FragCoord.x - (yScale * gl_FragCoord.y), spacing) > width) {
        discard;
    }

    fragColor = u_color;
}
