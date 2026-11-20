/*
 * lgmon's own avatars: eight little animals in five colourings and moods each, drawn here as SVG so they cost no
 * download and need no licence. Only the key ("lg-cat-3") travels between computers; each draws its own copy.
 * The keys must match internal/profile.Presets.
 */

export const critterSpecies = ['cat', 'bear', 'bunny', 'fox', 'panda', 'frog', 'chick', 'pig'] as const
type Species = (typeof critterSpecies)[number]
export const critterVariants = 5

export const critterKeys = critterSpecies.flatMap((species) =>
  Array.from({ length: critterVariants }, (_, index) => 'lg-' + species + '-' + (index + 1)),
)

const coats: Record<Species, string[]> = {
  cat: ['#F2A65A', '#A3A9B0', '#3A3836', '#F3E3CF', '#E9B44C'],
  bear: ['#A47551', '#8B5E3C', '#C9A27E', '#6E4B3A', '#D9B48F'],
  bunny: ['#F6F2EE', '#DCD2C9', '#F6D3DE', '#C9B7A7', '#E7E1F4'],
  fox: ['#E8743B', '#D9622B', '#F08A4B', '#C95A2A', '#E59A5C'],
  panda: ['#FAFAF7', '#FAFAF7', '#FAFAF7', '#FAFAF7', '#FAFAF7'],
  frog: ['#7BC67E', '#5FB36A', '#9AD48F', '#6CC4A1', '#8FCB6B'],
  chick: ['#FFD54F', '#FFCA28', '#FFE082', '#F9C02D', '#FFE57F'],
  pig: ['#F8B5C1', '#F4A3B4', '#F9C6CF', '#EFA0B0', '#FAD0D8'],
}

const grounds = ['#FDE7D9', '#E3F0FF', '#E6F6EA', '#FFF4CC', '#EFE7FF', '#FFE3EA', '#DFF5F4', '#F3EDE2']

const ink = '#2B2724'

function shade(hex: string, amount: number) {
  const value = parseInt(hex.slice(1), 16)
  const channel = (shift: number) => {
    const part = (value >> shift) & 255
    return Math.round(amount < 0 ? part * (1 + amount) : part + (255 - part) * amount)
  }
  return '#' + [16, 8, 0].map((shift) => channel(shift).toString(16).padStart(2, '0')).join('')
}

function dark(hex: string) {
  const value = parseInt(hex.slice(1), 16)
  return ((value >> 16) & 255) * 0.299 + ((value >> 8) & 255) * 0.587 + (value & 255) * 0.114 < 90
}

/* Two eyes and a mouth in one of five moods; light eyes on a dark coat, and lips of their own on a panda. */
function face(
  mood: number,
  eyeY: number,
  mouthY: number,
  gap: number,
  color: string,
  mouth = true,
  lips = color,
) {
  const left = 32 - gap,
    right = 32 + gap
  const dot = (x: number, r = 2.4) =>
    `<circle cx="${x}" cy="${eyeY}" r="${r}" fill="${color}"/><circle cx="${x + r * 0.35}" cy="${eyeY - r * 0.35}" r="${r * 0.32}" fill="#fff"/>`
  const arc = (x: number) =>
    `<path d="M${x - 2.6} ${eyeY + 1} Q${x} ${eyeY - 2} ${x + 2.6} ${eyeY + 1}" fill="none" stroke="${color}" stroke-width="1.6" stroke-linecap="round"/>`
  const line = (x: number) =>
    `<path d="M${x - 2.3} ${eyeY} h4.6" stroke="${color}" stroke-width="1.6" stroke-linecap="round"/>`
  const smile = `<path d="M${32 - 2.6} ${mouthY} Q32 ${mouthY + 2.6} ${32 + 2.6} ${mouthY}" fill="none" stroke="${lips}" stroke-width="1.5" stroke-linecap="round"/>`
  const eyes = [
    dot(left) + dot(right),
    arc(left) + arc(right),
    dot(left) + arc(right),
    line(left) + line(right),
    dot(left, 3) +
      dot(right, 3) +
      `<circle cx="${left - 1}" cy="${eyeY + 1}" r=".55" fill="#fff"/><circle cx="${right - 1}" cy="${eyeY + 1}" r=".55" fill="#fff"/>`,
  ][mood]
  const mouths = [
    smile,
    `<path d="M${32 - 2.8} ${mouthY - 0.6} Q32 ${mouthY + 4.4} ${32 + 2.8} ${mouthY - 0.6} Z" fill="#B5524A"/>`,
    smile,
    `<circle cx="32" cy="${mouthY + 0.6}" r="1.3" fill="${lips}"/>`,
    `<path d="M${32 - 3.2} ${mouthY - 0.4} Q${32 - 1.6} ${mouthY + 1.8} 32 ${mouthY - 0.4} Q${32 + 1.6} ${mouthY + 1.8} ${32 + 3.2} ${mouthY - 0.4}" fill="none" stroke="${lips}" stroke-width="1.4" stroke-linecap="round"/>`,
  ]
  return eyes + (mouth ? mouths[mood] : '')
}

const cheeks = (y: number, gap = 11.5) =>
  `<ellipse cx="${32 - gap}" cy="${y}" rx="3.2" ry="2" fill="#FF8A80" opacity=".38"/><ellipse cx="${32 + gap}" cy="${y}" rx="3.2" ry="2" fill="#FF8A80" opacity=".38"/>`

function draw(species: Species, coat: string, mood: number) {
  const eye = dark(coat) ? '#FFE9A8' : ink
  const head = (rx = 19, ry = 17, cy = 38) =>
    `<ellipse cx="32" cy="${cy}" rx="${rx}" ry="${ry}" fill="${coat}"/>`
  switch (species) {
    case 'cat': {
      const inner = dark(coat) ? '#6B5E5A' : '#F7B8C4'
      return (
        `<path d="M15 31 L19 13 L29 24 Z M49 31 L45 13 L35 24 Z" fill="${coat}" stroke="${coat}" stroke-width="3" stroke-linejoin="round"/>` +
        `<path d="M18.6 26 L20.6 17.5 L25.6 22.8 Z M45.4 26 L43.4 17.5 L38.4 22.8 Z" fill="${inner}"/>` +
        head() +
        cheeks(43) +
        `<path d="M30.6 41.4 h2.8 l-1.4 1.6 Z" fill="${dark(coat) ? '#F7B8C4' : '#E07A8A'}"/>` +
        face(mood, 37, 45, 7.5, eye) +
        `<path d="M13 42 h6 M13.5 45.5 l5.5 -1.2 M51 42 h-6 M50.5 45.5 l-5.5 -1.2" stroke="${shade(coat, dark(coat) ? 0.45 : -0.35)}" stroke-width=".9" stroke-linecap="round"/>`
      )
    }
    case 'bear':
      return (
        `<circle cx="18" cy="24" r="7" fill="${coat}"/><circle cx="46" cy="24" r="7" fill="${coat}"/>` +
        `<circle cx="18" cy="24" r="3.6" fill="${shade(coat, 0.35)}"/><circle cx="46" cy="24" r="3.6" fill="${shade(coat, 0.35)}"/>` +
        head() +
        `<ellipse cx="32" cy="44.5" rx="7.5" ry="5.6" fill="${shade(coat, 0.45)}"/>` +
        `<ellipse cx="32" cy="41.8" rx="2.7" ry="1.9" fill="${ink}"/>` +
        cheeks(42, 12.5) +
        face(mood, 35.5, 45.2, 7.5, ink)
      )
    case 'bunny':
      return (
        `<ellipse cx="24" cy="15" rx="5.2" ry="13" fill="${coat}" transform="rotate(-12 24 15)"/><ellipse cx="40" cy="15" rx="5.2" ry="13" fill="${coat}" transform="rotate(12 40 15)"/>` +
        `<ellipse cx="24" cy="16" rx="2.4" ry="9" fill="#F4A9BA" transform="rotate(-12 24 16)"/><ellipse cx="40" cy="16" rx="2.4" ry="9" fill="#F4A9BA" transform="rotate(12 40 16)"/>` +
        head(18.5, 16.5, 39) +
        cheeks(44) +
        `<ellipse cx="32" cy="42.4" rx="1.8" ry="1.3" fill="#E58A9C"/>` +
        face(mood, 37.5, 45.6, 7.2, ink)
      )
    case 'fox':
      return (
        `<path d="M13 33 L16.5 10 L30 22 Z M51 33 L47.5 10 L34 22 Z" fill="${coat}" stroke="${coat}" stroke-width="3" stroke-linejoin="round"/>` +
        `<path d="M16.6 26 L18.4 15.5 L25 21.6 Z M47.4 26 L45.6 15.5 L39 21.6 Z" fill="#3B2A20" opacity=".75"/>` +
        head(20, 17) +
        `<path d="M12.5 39 Q22 40 32 50 Q42 40 51.5 39 Q50 53 32 55 Q14 53 12.5 39 Z" fill="#FFF8F0"/>` +
        `<ellipse cx="32" cy="45.2" rx="2.4" ry="1.7" fill="${ink}"/>` +
        face(mood, 36, 48.6, 8, ink)
      )
    case 'panda':
      return (
        `<circle cx="17" cy="23.5" r="6.8" fill="${ink}"/><circle cx="47" cy="23.5" r="6.8" fill="${ink}"/>` +
        head() +
        `<ellipse cx="24.6" cy="37.5" rx="5.2" ry="6.6" fill="${ink}" transform="rotate(-22 24.6 37.5)"/><ellipse cx="39.4" cy="37.5" rx="5.2" ry="6.6" fill="${ink}" transform="rotate(22 39.4 37.5)"/>` +
        `<ellipse cx="32" cy="43.2" rx="2.5" ry="1.7" fill="${ink}"/>` +
        cheeks(45.5, 13) +
        face(mood, 37.5, 47, 7.4, '#FAFAF7', true, ink)
      )
    case 'frog':
      return (
        `<circle cx="21.5" cy="26" r="8" fill="${coat}"/><circle cx="42.5" cy="26" r="8" fill="${coat}"/>` +
        head(21, 15.5, 40) +
        `<circle cx="21.5" cy="26" r="5.2" fill="#fff"/><circle cx="42.5" cy="26" r="5.2" fill="#fff"/>` +
        face(mood, 26, 46, 10.5, ink, false) +
        cheeks(44, 13.5) +
        [
          `<path d="M24 44.5 Q32 50 40 44.5" fill="none" stroke="${ink}" stroke-width="1.5" stroke-linecap="round"/>`,
          `<path d="M24 44 Q32 53 40 44 Z" fill="#B5524A"/>`,
          `<path d="M24 44.5 Q32 50 40 44.5" fill="none" stroke="${ink}" stroke-width="1.5" stroke-linecap="round"/>`,
          `<ellipse cx="32" cy="46" rx="2" ry="1.4" fill="${ink}"/>`,
          `<path d="M24 45 Q28 48.5 32 45 Q36 48.5 40 45" fill="none" stroke="${ink}" stroke-width="1.4" stroke-linecap="round"/>`,
        ][mood]
      )
    case 'chick':
      return (
        `<path d="M29 21 Q31 12 33 20 Q34.6 13.5 37 19" fill="none" stroke="${shade(coat, -0.25)}" stroke-width="2.2" stroke-linecap="round"/>` +
        `<circle cx="32" cy="38" r="18.5" fill="${coat}"/>` +
        `<path d="M14.5 40 Q11 46 16 49 Q18 45 17.5 41 Z M49.5 40 Q53 46 48 49 Q46 45 46.5 41 Z" fill="${shade(coat, -0.12)}"/>` +
        cheeks(43, 11) +
        face(mood, 36.5, 46, 7, ink, false) +
        `<path d="M28.6 41.6 L35.4 41.6 L32 45.6 Z" fill="#FF9800" stroke="#FF9800" stroke-width="1.2" stroke-linejoin="round"/>`
      )
    case 'pig':
      return (
        `<path d="M17 28 L20 16 L28 23 Z M47 28 L44 16 L36 23 Z" fill="${shade(coat, -0.08)}" stroke="${shade(coat, -0.08)}" stroke-width="2.5" stroke-linejoin="round"/>` +
        head() +
        `<ellipse cx="32" cy="43" rx="6.8" ry="4.6" fill="${shade(coat, -0.14)}"/>` +
        `<ellipse cx="29.8" cy="43" rx="1.1" ry="1.7" fill="#B9566C"/><ellipse cx="34.2" cy="43" rx="1.1" ry="1.7" fill="#B9566C"/>` +
        cheeks(42.5, 13.5) +
        face(mood, 35, 49.6, 8, ink)
      )
  }
}

const drawn = new Map<string, string>()

/* The picture for a critter key, as an SVG data URL, or "" for anything else. */
export function critterSrc(key: string) {
  const cached = drawn.get(key)
  if (cached !== undefined) return cached
  const match = /^lg-([a-z]+)-([1-5])$/.exec(key)
  const species = match?.[1] as Species | undefined
  if (!match || !species || !critterSpecies.includes(species)) return ''
  const variant = Number(match[2]) - 1
  const ground = grounds[(critterSpecies.indexOf(species) * 3 + variant) % grounds.length]
  const svg =
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">' +
    `<rect width="64" height="64" fill="${ground}"/>` +
    draw(species, coats[species][variant], variant) +
    '</svg>'
  const url = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg)
  drawn.set(key, url)
  return url
}
