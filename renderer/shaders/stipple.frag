#version 330 core

uniform int u_pattern; // 1=light, 2=dense
uniform vec4 u_color;

out vec4 fragColor;

// Exact STARS weather stipple masks used by VICE. The bit strings are kept in
// their intended on-screen orientation; bit 31 is the leftmost pixel.
const uint lightPattern[32] = uint[32](
    0x00000000u, 0x00000000u, 0x000C0000u, 0x000C0000u,
    0x00000000u, 0x00000000u, 0x00000000u, 0x00000300u,
    0x00000300u, 0x00000000u, 0x00000000u, 0x01800000u,
    0x01800000u, 0x00000000u, 0x00000000u, 0x00030000u,
    0x00030000u, 0x0000000Cu, 0x0000000Cu, 0x00000000u,
    0x00000000u, 0x00000000u, 0x00C00000u, 0x00C00000u,
    0x00000000u, 0x00003000u, 0x00003000u, 0x00000000u,
    0x00000000u, 0x00000000u, 0xC0000000u, 0xC0000000u
);

const uint densePattern[32] = uint[32](
    0x00000000u, 0x00000000u, 0x08000800u, 0x08000800u,
    0x00180018u, 0x40004000u, 0x40004000u, 0x01800180u,
    0x00000000u, 0x00030003u, 0x00000000u, 0x18001800u,
    0x00000000u, 0x00200020u, 0x00200020u, 0xC000C000u,
    0x00000000u, 0x00000000u, 0x08000800u, 0x08000800u,
    0x00180018u, 0x40004000u, 0x40004000u, 0x01800180u,
    0x00000000u, 0x00030003u, 0x00000000u, 0x18001800u,
    0x00000000u, 0x00200020u, 0x00200020u, 0xC000C000u
);

void main() {
    int x = int(floor(gl_FragCoord.x)) & 31;
    int y = int(floor(gl_FragCoord.y)) & 31;
    uint row = u_pattern == 1 ? lightPattern[y] : densePattern[y];
    uint mask = 1u << uint(31 - x);
    if ((row & mask) == 0u) {
        discard;
    }
    fragColor = u_color;
}
