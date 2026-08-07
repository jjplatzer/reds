#version 330 core

uniform vec2 u_offset;
uniform vec4 u_color;

out vec4 fragColor;

void main() {
    int xIndex = int(gl_FragCoord.x + u_offset.x) / 4;
    int yIndex = int(gl_FragCoord.y + u_offset.y) / 4;

    if ((xIndex + yIndex) % 2 == 0) {
        discard;
    }

    fragColor = u_color;
}
