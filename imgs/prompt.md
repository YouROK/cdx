Create a logo for the cdx project with the photocraft MCP server. Work only with
the mcp_photocraft_* tools and only inside ~/Documents/photocraft-test.

First check the available commands (command_list or the tool list) and then use
only the ones that actually exist. Do not touch files outside that folder and do
not overwrite existing ones - if a name is taken, add a -v2 suffix.

Brief:
- Canvas 1024x1024, fully dark background #0E1116 (no transparency).
- Style: minimalist, "terminal plus geometry", no heavy shadows and no
  photorealism.
- Composition in the center:
  1) the terminal prompt mark ">_" on top in #2F7BF5, monospace font, size
     about 220 px;
  2) the word "cdx" below it in white #F5F7FA, the same monospace font, bold,
     size about 300 px, letter spacing slightly tighter than normal;
  3) a thin accent line #2F7BF5 under the word: about 55% of the canvas wide,
     10 px high, rounded ends where supported.
- Everything centered, equal margins; the mark and the word together should take
  about 60% of the canvas height.
- Name the layers meaningfully: background, prompt-mark, wordmark, accent-line.
- Font: JetBrains Mono if it is in the font list, otherwise DejaVu Sans Mono,
  otherwise any monospace font.

Saving and checking:
- Export a 1024x1024 PNG to ~/Documents/photocraft-test/cdx-logo.png.
- If supported, also save an editable cdx-logo.psd or cdx-logo.pcraft next to it.
- Run render_preview and check that the text is not clipped, the line is
  centered, and the background is even.
- In the answer list: the file paths, the sizes, the font used, the layer list,
  and what had to be replaced if a command was unavailable.

If an operation is not supported, do not stop: pick the closest working command
and state explicitly what you replaced it with.
