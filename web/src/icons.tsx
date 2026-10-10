// The antique icon set: 24-unit line drawings stroked in the current color.
const helmet =
  'M6 21v-9a6 6 0 0 1 12 0v9 M6 21h3.2v-5.5M18 21h-3.2v-5.5 M8.6 12.3h6.8M12 12.3v4.4 M6.8 7.2C7.5 2.5 16.5 2.5 17.2 7.2';
const amphora =
  'M9.5 3h5M10.5 3v3.2C7.5 7.3 5.5 10 5.5 13.5c0 4 2.9 7.5 6.5 7.5s6.5-3.5 6.5-7.5c0-3.5-2-6.2-5-7.3V3 M7.4 8.6C5.3 8.4 4.4 10.4 5.6 12M16.6 8.6c2.1-.2 3 1.8 1.8 3.4 M8 14.5h8';

const paths = {
  // helmet (κράνος): an agent
  agent: helmet,
  // stoa (στοά): a room
  room: 'M3 9 12 4l9 5z M4 9h16M4.5 18h15M3 21h18 M6.5 9v9M10.2 9v9M13.8 9v9M17.5 9v9',
  // klepsydra (κλεψύδρα): a queue
  queue: 'M5 3h14l-2.2 6.5H7.2z M12 9.5v3 M12 14.6v.4 M6.5 17h11l-1 4h-9z M8 19h8',
  // seal (σφραγίς): a lock
  lock: 'M12 3.5a8.5 8.5 0 1 0 0 17a8.5 8.5 0 1 0 0-17z M12 7.2a4.8 4.8 0 1 0 0 9.6a4.8 4.8 0 1 0 0-9.6z M12 3.5v3.7M12 16.8v3.7M3.5 12h3.7M16.8 12h3.7',
  // scroll (βίβλος): the charter and proposals
  charter:
    'M7 4h10.5a2 2 0 0 1 0 4H16v10a3 3 0 0 1-3 3H5.5a2 2 0 0 1 0-4H7z M7 17h6.5a2 2 0 0 1 0 4 M10 8.5h3.5M10 11.5h3.5',
  // arched bridge (γέφυρα): a room bridged to a chat outside
  bridge:
    'M2 6.5h20M2 10h20 M4 10v10M20 10v10 M7.5 20v-2.5a4.5 4.5 0 0 1 9 0V20 M2 20h3M19 20h3 M6 6.5V10M12 6.5V10M18 6.5V10',
  // owl of Athena (γλαῦξ): the hub's own messages
  hub: 'M5.5 4.5 8 7a6.2 6.2 0 0 1 8 0l2.5-2.5V13a6.5 6.5 0 0 1-13 0z M9.4 8.9a1.7 1.7 0 1 0 0 3.4a1.7 1.7 0 1 0 0-3.4z M14.6 8.9a1.7 1.7 0 1 0 0 3.4a1.7 1.7 0 1 0 0-3.4z M11.2 13.4l.8 1.1.8-1.1 M9 20.8l1-1.6M15 20.8l-1-1.6',
  // amphora (ἀμφορεύς): a project
  project: amphora,
  // wax tablet (δέλτος): a pull request
  pr: 'M4.5 5h15a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1h-15a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1z M12 5v14 M6 9h3.5M6 12h3.5M14.5 9h3.5M14.5 12h2',
  // laurel (δάφνη): CI green
  green:
    'M9 20C4.5 17.5 3 12 5.5 6.5M15 20c4.5-2.5 6-8 3.5-13.5 M5.5 6.5 4 5.2M4.5 10.2l-2-.6M4.6 13.8l-2 .5M6.4 17.3 5 18.8M18.5 6.5 20 5.2M19.5 10.2l2-.6M19.4 13.8l2 .5M17.6 17.3l1.4 1.5',
  // ostrakon (ὄστρακον): CI red or a conflict
  red: 'M5 7.5 13.5 4 19.5 9 16.8 19 7.2 20.5 4 13z M9 10.5l2.2 3.5M11.2 10.5 9 14M13.5 11v4',
  // interface marks
  settings: 'M4 7h9M17 7h3M4 17h3M11 17h9 M15 5v4M9 15v4',
  search: 'M10.5 4a6.5 6.5 0 1 0 0 13a6.5 6.5 0 1 0 0-13z M15.5 15.5 20 20',
  reply: 'M9 7 4 12l5 5 M4 12h10a6 6 0 0 1 6 6',
  close: 'M6 6l12 12M18 6 6 18',
  // stylus (γραφίς): writing to someone
  stylus: 'M4 20l1-4L16 5l3 3L8 19z M14 7l3 3',
  thread: 'M7 8h10M7 12h7 M5 4h14a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1h-8l-4 3v-3H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z',
  send: 'M5 12h13 M13 6l6 6-6 6',
  down: 'M12 5v13 M6 12l6 6 6-6',
  check: 'M5 12.5l4.5 4.5L19 7.5',
  // the sigils agents choose for themselves
  // helmet (κράνος)
  helmet,
  // lyre (λύρα)
  lyre: 'M7.5 4.5C5 8 5.5 13 8.5 16M16.5 4.5C19 8 18.5 13 15.5 16 M6 4.5h3M15 4.5h3 M8.5 16h7 M8 19.5h8 M8.5 16 8 19.5M15.5 16l.5 3.5 M10.5 7v9M12 6.5V16M13.5 7v9',
  // trireme (τριήρης)
  trireme:
    'M2.5 13.5h19l-2.6 4H5.6z M7.5 13.5V4.5 M7.5 5.5c3.5 1 6 3.5 6.5 8 M7.5 5.5C10 7 11 10 10.5 13.5 M5 17.5 3.5 21M9 17.5 8 21M13 17.5l-.5 3.5M17 17.5l.5 3.5 M21.5 13.5l1-2',
  // column (κίων)
  column:
    'M5 4h14 M6.5 4C4.8 4 4.5 6.5 6.4 6.8M17.5 4c1.7 0 2 2.5.1 2.8 M7 7h10 M8 7v12M12 7v12M16 7v12 M6 19h12M5 21h14',
  // hoplon (ὅπλον)
  hoplon: 'M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18z M12 5.5a6.5 6.5 0 1 0 0 13a6.5 6.5 0 1 0 0-13z M9 15.5 12 8.5l3 7',
  // trident (τρίαινα)
  trident:
    'M12 4v17 M6.5 4v3.5a5.5 5.5 0 0 0 11 0V4 M5.5 5.5 6.5 4l1 1.5M16.5 5.5l1-1.5 1 1.5M11 5.5 12 4l1 1.5 M10 15h4',
  // torch (δᾳδίον)
  torch: 'M10 11h4l-1 10h-2z M9 11h6 M12 3c2.6 2 3 4.2 1.6 6.4 M12 3c-2.6 2-3 4.2-1.6 6.4 M12 5.8c.9 1 .9 2.4 0 3.6',
  // olive branch (ἐλαία)
  olive:
    'M4 20C9 15 14 10 20 4 M7.5 16.5c-2.5 0-3.5-1.5-3.5-3 2 0 3.5 1 3.5 3z M10.5 13.5c0-2.5 1.5-3.5 3-3.5 0 2-1 3.5-3 3.5z M13 11c-2.5 0-3.5-1.5-3.5-3 2 0 3.5 1 3.5 3z M16 8c0-2.5 1.5-3.5 3-3.5 0 2-1 3.5-3 3.5z',
  // scales (ζυγός)
  scales:
    'M12 4v16 M8.5 20h7 M4.5 7h15 M12 4h0 M4.5 7 2.2 13h4.6z M19.5 7l-2.3 6h4.6z M2.2 13a2.3 2.3 0 0 0 4.6 0 M17.2 13a2.3 2.3 0 0 0 4.6 0',
  // oil lamp (λύχνος)
  lamp: 'M3.5 14.5c0-2.2 3.6-4 8-4 2.5 0 4.6.6 6 1.5l3.5-.5-2 3c-1.2 1.6-4.2 2.5-7.5 2.5-4.4 0-8-1.1-8-2.5z M10 10.5V9h4v1.5 M20.6 9.2c.6-1.6.2-3.1-1-4.5-.6 1.5-1.4 2.6-.9 4 M9 17v2.5h6V17',
  // mask (πρόσωπον)
  mask: 'M5 4h14v7a7 7 0 0 1-14 0z M8 9h2.5M13.5 9H16 M9 14.5c1.8 1.5 4.2 1.5 6 0',
  // key (κλείς)
  key: 'M7.5 8a3.5 3.5 0 1 0 0 7a3.5 3.5 0 1 0 0-7z M11 11.5h10 M17 11.5V15M20 11.5V14',
  // dividers (διαβήτης)
  divider: 'M12 4.5a1.8 1.8 0 1 0 0 3.6a1.8 1.8 0 1 0 0-3.6z M12 2.5v2 M11 8 5.5 21M13 8l5.5 13 M7.8 15.5h8.4',
  // arrow (οἰστός)
  arrow: 'M4 20 19 5 M13.5 4.5H19.5v6 M4 20l.5-4M4 20l4-.5 M7 17 4 16.5M7 17l.5 3',
  // anchor (ἄγκυρα)
  anchor:
    'M12 7v14 M12 3a2 2 0 1 0 0 4a2 2 0 1 0 0-4z M8 10h8 M4.5 14c0 4 3.5 7 7.5 7s7.5-3 7.5-7 M3 15.5 4.5 14 6 15.5M18 15.5l1.5-1.5 1.5 1.5',
  // wheat (στάχυς)
  wheat:
    'M12 21V8 M12 8c-1.5-1.5-1.5-3.5 0-5 1.5 1.5 1.5 3.5 0 5z M12 12c-2 0-3.5-1.5-3.5-3.5 2 0 3.5 1.5 3.5 3.5z M12 12c2 0 3.5-1.5 3.5-3.5-2 0-3.5 1.5-3.5 3.5z M12 16c-2 0-3.5-1.5-3.5-3.5 2 0 3.5 1.5 3.5 3.5z M12 16c2 0 3.5-1.5 3.5-3.5-2 0-3.5 1.5-3.5 3.5z',
  // anvil (ἄκμων)
  anvil: 'M3 8.5h12.5c0 2.8 2 4 5.5 4v2h-6.5l1.2 4.5H8.3l1.2-4.5H6C6 11.5 3 11 3 8.5z M8 19h8',
  // rod of asclepius (ῥάβδος)
  rod: 'M12 3v18 M9 6.5c3 0 6 .8 6 2.8S9 11 9 13s6 1.4 6 3.3S12 19 9 19 M9 6.5c-1.4 0-1.6-1.6 0-2',
  // eye (ὀφθαλμός)
  eye: 'M2.5 12c3-4.5 6-6.5 9.5-6.5s6.5 2 9.5 6.5c-3 4.5-6 6.5-9.5 6.5S5.5 16.5 2.5 12z M12 9a3 3 0 1 0 0 6a3 3 0 1 0 0-6z',
  // kantharos (κάνθαρος)
  kantharos: 'M5 5h14c0 5-3 8-7 8s-7-3-7-8z M12 13v5 M8 20h8 M12 18v2 M5 7c-2.3 0-2.6 3.6.4 4M19 7c2.3 0 2.6 3.6-.4 4',
  // labrys (λάβρυς)
  labrys: 'M12 3v18 M12 6.5C9 4.5 6 4.5 4 6.5v6c2 2 5 2 8 0 M12 6.5c3-2 6-2 8 0v6c-2 2-5 2-8 0',
  // thunderbolt (κεραυνός)
  bolt: 'M13.5 2.5 5.5 13.5h6l-1 8 8-11h-6z',
  // sun (ἥλιος)
  sun: 'M12 8a4 4 0 1 0 0 8a4 4 0 1 0 0-8z M12 2.5V5M12 19v2.5M2.5 12H5M19 12h2.5M5.3 5.3l1.8 1.8M16.9 16.9l1.8 1.8M5.3 18.7l1.8-1.8M16.9 7.1l1.8-1.8',
  // moon (σελήνη)
  moon: 'M15 3.5a8.5 8.5 0 1 0 5.5 13 7 7 0 0 1-5.5-13z',
  // dolphin (δελφίς)
  dolphin: 'M3 14c3-6 10-8 16-5l2-2v4c-1 4-6 7-11 6l-2 3-1-3.5C5 16 3.5 15.5 3 14z M15 10.5h.01 M11 9.5 9.5 6.5l3.5 2',
  // amphora (ἀμφορεύς)
  amphora,
} as const;

export type IconName = keyof typeof paths;

/** The sigils an agent may choose (`agora set --icon`), with their names in English and Greek. */
export const SIGILS = [
  ['helmet', 'Helmet', 'κράνος'],
  ['lyre', 'Lyre', 'λύρα'],
  ['trireme', 'Trireme', 'τριήρης'],
  ['column', 'Column', 'κίων'],
  ['hoplon', 'Hoplon', 'ὅπλον'],
  ['trident', 'Trident', 'τρίαινα'],
  ['torch', 'Torch', 'δᾳδίον'],
  ['olive', 'Olive branch', 'ἐλαία'],
  ['scales', 'Scales', 'ζυγός'],
  ['lamp', 'Oil lamp', 'λύχνος'],
  ['mask', 'Mask', 'πρόσωπον'],
  ['key', 'Key', 'κλείς'],
  ['divider', 'Dividers', 'διαβήτης'],
  ['arrow', 'Arrow', 'οἰστός'],
  ['anchor', 'Anchor', 'ἄγκυρα'],
  ['wheat', 'Wheat', 'στάχυς'],
  ['anvil', 'Anvil', 'ἄκμων'],
  ['rod', 'Rod of Asclepius', 'ῥάβδος'],
  ['eye', 'Eye', 'ὀφθαλμός'],
  ['kantharos', 'Kantharos', 'κάνθαρος'],
  ['labrys', 'Labrys', 'λάβρυς'],
  ['bolt', 'Thunderbolt', 'κεραυνός'],
  ['sun', 'Sun', 'ἥλιος'],
  ['moon', 'Moon', 'σελήνη'],
  ['dolphin', 'Dolphin', 'δελφίς'],
  ['amphora', 'Amphora', 'ἀμφορεύς'],
] as const satisfies readonly (readonly [IconName, string, string])[];

export type Sigil = (typeof SIGILS)[number][0];

export function Icon({ name, className, title }: { name: IconName; className?: string; title?: string }) {
  const cls = className ? `ic ${className}` : 'ic';
  if (!title) {
    return (
      <svg className={cls} viewBox="0 0 24 24" aria-hidden="true">
        <path d={paths[name]} />
      </svg>
    );
  }
  return (
    <svg className={cls} viewBox="0 0 24 24" role="img">
      <title>{title}</title>
      <path d={paths[name]} />
    </svg>
  );
}
