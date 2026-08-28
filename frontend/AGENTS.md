# AGENTS.md

Project-specific guidance for AI coding agents.

<!-- ASTRYX:START -->
Astryx v0.5.0 · 163 components
CLI: run every command as `npx astryx <cmd>` (shown below as `astryx ...`).

SETUP (once, in your app entry e.g. main.tsx) — without these, components render unstyled:
  import "@astryxdesign/core/reset.css";
  import "@astryxdesign/core/astryx.css";

WORKFLOW — discover, don't guess. Before writing UI:
1. `astryx build "<idea>"` — START HERE: returns a kit (closest [page] + [block]s + [component]s). No args = full playbook.
2. `astryx template <name> [--skeleton]` — scaffold the [page]/[block]s it named, or study their layout. Templates are reference code.
3. `astryx component <Name>` — props + examples for every component you use.

RULES:
- No <div> — components do all layout/spacing, page frame included.
- Frame first: read `astryx docs layout` before writing any page or screen — page frame, region widths, breakpoint behavior.
- Dense data = rows (Table, List/Item), never Card-wrapped list items; Card is for standalone widgets. Status = StatusDot/Token; Badge = counts only.
- Custom styling: component props first; else Tailwind utilities backed by tokens (bg-surface, text-primary, rounded-lg) via tailwind-theme.css. No raw hex/px.
- Tokens for every value (`astryx docs tokens`). Brand/accent belongs in the theme (`astryx theme list` / `theme add <slug>`, or `astryx theme template` for a custom one) — never override --color-* in :root.
- SELF-CHECK before you finish: re-read the file and replace any style={{…}}, raw <div>/<span> layout, imported .css/@apply, or hardcoded/arbitrary value (e.g. bg-[#fff], p-[13px]) with the component or a token-backed utility. If unsure a component/prop exists, run `astryx component <Name>` / `astryx search "<thing>"`; don't hand-roll CSS.

MORE CLI:
  search "<query>"   find any component / hook / doc / template / block
  component --list   163 components by category
  template --list    page + block recipes
  docs <topic>       browser-support, cli-integrations, color, elevation, getting-started, icons, illustrations, internationalization, layout, migration, motion, principles, shape, spacing, styling-libraries, styling, theme, tokens, typography, working-with-ai
  swizzle <Name>     eject component source for deep customization
  upgrade --apply    run after any @astryxdesign/core bump
<!-- ASTRYX:END -->

## Cosmo UI writing rules

Keep explanation out of the interface. The screen shows what something *is* and
what its current state *is* — not how it works or why.

- **No teaching copy.** Drop section blurbs that restate the feature ("Mỗi
  workspace dùng khoá riêng…"), page subtitles, and implementation notes ("Khoá
  được mã hoá AES-256-GCM…"). That belongs in code comments, this file, or the
  README.
- **Placeholders use neutral references, never invented ones.** An example is
  fine — it makes the expected shape concrete in a way a label cannot. What is
  not fine is making up a company or a person: `llm.congty.vn`,
  `tenban@congty.vn`, `Khối Vận hành Sản xuất`, `Nguyễn Văn An` all read as
  filler and look unprofessional. Use a real vendor the shape actually comes
  from (`https://api.openai.com/v1`, `sk-...`) or the reserved example domain
  (`name@example.com`).
- **Skip the placeholder when no neutral example exists.** A workspace name or a
  person's name has no canonical example, so leave the field empty and let the
  label do the work.
- **A placeholder still is not a home for deleted hints.** It shows a value, not
  a sentence. The two exceptions are a rule the user cannot infer and will
  otherwise fail on ("Tối thiểu 10 ký tự, gồm chữ và số") and a control with no
  visible label, where the placeholder *is* the label (the chat composer).
- **Keep state and consequence.** "Đang lưu ••••a1b2", "Link mời — chỉ hiện một
  lần", an error message, or a disabled reason all tell the user something they
  cannot see otherwise. Keep those, and keep them short.
- **Prefer doing over explaining.** If a field needs a paragraph to use, change
  the control: the model field fetches its options from the gateway rather than
  telling the operator to press a test button and type an identifier.
- **A heading plus the control is usually enough.** Reach for a description only
  when leaving it out would make the control ambiguous.
