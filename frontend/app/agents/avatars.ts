// The emoji offered as an agent avatar, grouped the way a picker reads.
//
// Every entry is a single code point on purpose. Avatar renders the first
// character of the name it is given, and a sequence joined by ZWJ or trailed
// by a variation selector would be cut in half - the flag would lose its
// second half, the profession its person. Keywords drive the search box, so a
// Vietnamese reader can type "sach" or "book" and reach the same emoji.
//
// Every entry is also colour by default. An emoji whose default is text
// presentation needs a U+FE0F this list cannot carry, and would render as a
// flat black glyph beside the coloured ones.
export type EmojiGroup = {id: string; emoji: {char: string; keywords: string}[]};

export const EMOJI_GROUPS: EmojiGroup[] = [
  {
    id: 'work',
    emoji: [
      {char: '🤖', keywords: 'robot bot may ai'},
      {char: '🧠', keywords: 'brain nao tri tue'},
      {char: '💡', keywords: 'idea y tuong den bulb'},
      {char: '🔍', keywords: 'search tim kiem'},
      {char: '📊', keywords: 'chart bieu do bao cao'},
      {char: '📈', keywords: 'growth tang truong'},
      {char: '📁', keywords: 'files ho so tai lieu folder'},
      {char: '📋', keywords: 'clipboard danh sach'},
      {char: '📝', keywords: 'note ghi chu soan'},
      {char: '🧾', keywords: 'receipt hoa don chung tu'},
      {char: '📌', keywords: 'pin ghim'},
      {char: '📎', keywords: 'tag nhan the dinh kem'},
    ],
  },
  {
    id: 'knowledge',
    emoji: [
      {char: '📚', keywords: 'books sach thu vien'},
      {char: '📖', keywords: 'book doc sach'},
      {char: '🎓', keywords: 'graduate hoc dao tao'},
      {char: '🧭', keywords: 'compass la ban huong dan'},
      {char: '🌐', keywords: 'map ban do tong quan'},
      {char: '🔖', keywords: 'bookmark danh dau'},
      {char: '🧩', keywords: 'puzzle manh ghep'},
      {char: '🔬', keywords: 'microscope nghien cuu'},
      {char: '🧪', keywords: 'lab thi nghiem kiem thu'},
      {char: '🔭', keywords: 'research quan sat nghien cuu'},
      {char: '🔨', keywords: 'tools cong cu sua chua'},
      {char: '🔧', keywords: 'wrench co khi bao tri'},
    ],
  },
  {
    id: 'industry',
    emoji: [
      {char: '🏭', keywords: 'factory nha may san xuat'},
      {char: '🔥', keywords: 'oil dau lua nhien lieu dot'},
      {char: '⛽', keywords: 'fuel xang nhien lieu'},
      {char: '🧰', keywords: 'gear toolbox van hanh bao tri'},
      {char: '🔩', keywords: 'bolt oc vit thiet bi'},
      {char: '🧯', keywords: 'extinguisher chua chay an toan'},
      {char: '🚨', keywords: 'safety canh bao an toan'},
      {char: '⚡', keywords: 'power dien nang luong'},
      {char: '📟', keywords: 'device thiet bi do nhiet'},
      {char: '🚧', keywords: 'construction thi cong canh bao'},
      {char: '🧱', keywords: 'brick xay dung'},
      {char: '🚛', keywords: 'truck van chuyen logistics'},
    ],
  },
  {
    id: 'people',
    emoji: [
      {char: '🙂', keywords: 'smile vui than thien'},
      {char: '🤝', keywords: 'handshake hop tac ho tro'},
      {char: '💬', keywords: 'chat tro chuyen hoi dap'},
      {char: '📣', keywords: 'announce thong bao'},
      {char: '🎯', keywords: 'target muc tieu'},
      {char: '🏆', keywords: 'trophy thanh tich giai'},
      {char: '⭐', keywords: 'star sao noi bat'},
      {char: '🔔', keywords: 'bell nhac chuong'},
      {char: '📅', keywords: 'calendar lich ke hoach'},
      {char: '⏰', keywords: 'timer thoi gian bao thuc'},
      {char: '🔒', keywords: 'lock bao mat khoa'},
      {char: '✅', keywords: 'check duyet hoan thanh'},
    ],
  },
];

// searchEmoji matches the character itself as well as its keywords, so pasting
// an emoji finds it and typing a word finds it too.
export function searchEmoji(query: string): {char: string; keywords: string}[] {
  const needle = query.trim().toLowerCase();
  const all = EMOJI_GROUPS.flatMap((group) => group.emoji);
  if (!needle) return [];
  return all.filter((item) => item.char === needle || item.keywords.includes(needle));
}
