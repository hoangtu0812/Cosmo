/**
 * The Model Context Protocol mark.
 *
 * A puzzle-piece emoji stood in for it, which says "plugin" - the thing every
 * integration in this list is. What a reader needs from the icon is the one
 * thing that is different about these: the server describes itself, so its
 * actions were discovered rather than typed. That is what the MCP mark says,
 * and a reader who has seen it anywhere else recognises it here.
 *
 * The three paths are the official mark from the protocol's own repository
 * (modelcontextprotocol/modelcontextprotocol, docs/logo), taken verbatim rather
 * than redrawn - an approximation of somebody's mark is worse than no mark. The
 * wordmark beside it in that file is dropped; only the glyph is wanted.
 *
 * The one change is the stroke: black in the original, currentColor here, so it
 * takes the colour of the text around it and reads in both themes.
 */
export function McpMark({size = 32}: {size?: number}) {
  return (
    <svg
      aria-hidden
      fill="none"
      height={size}
      role="img"
      viewBox="0 0 195 195"
      width={size}
      xmlns="http://www.w3.org/2000/svg"
    >
      <path
        d="M25 97.8528L92.8823 29.9706C102.255 20.598 117.451 20.598 126.823 29.9706V29.9706C136.196 39.3431 136.196 54.5391 126.823 63.9117L75.5581 115.177"
        stroke="currentColor"
        strokeLinecap="round"
        strokeWidth={12}
      />
      <path
        d="M76.2653 114.47L126.823 63.9117C136.196 54.5391 151.392 54.5391 160.765 63.9117L161.118 64.2652C170.491 73.6378 170.491 88.8338 161.118 98.2063L99.7248 159.6C96.6006 162.724 96.6006 167.789 99.7248 170.913L112.331 183.52"
        stroke="currentColor"
        strokeLinecap="round"
        strokeWidth={12}
      />
      <path
        d="M109.853 46.9411L59.6482 97.1457C50.2757 106.518 50.2757 121.714 59.6482 131.087V131.087C69.0208 140.459 84.2168 140.459 93.5894 131.087L143.794 80.8822"
        stroke="currentColor"
        strokeLinecap="round"
        strokeWidth={12}
      />
    </svg>
  );
}
