// The antique icon set: 24-unit line drawings stroked in the current color.
const paths = {
  // helmet (κράνος): an agent
  agent:
    'M6 21v-9a6 6 0 0 1 12 0v9 M6 21h3.2v-5.5M18 21h-3.2v-5.5 M8.6 12.3h6.8M12 12.3v4.4 M6.8 7.2C7.5 2.5 16.5 2.5 17.2 7.2',
  // stoa (στοά): a room
  room: 'M3 9 12 4l9 5z M4 9h16M4.5 18h15M3 21h18 M6.5 9v9M10.2 9v9M13.8 9v9M17.5 9v9',
  // klepsydra (κλεψύδρα): a queue
  queue: 'M5 3h14l-2.2 6.5H7.2z M12 9.5v3 M12 14.6v.4 M6.5 17h11l-1 4h-9z M8 19h8',
  // seal (σφραγίς): a lock
  lock: 'M12 3.5a8.5 8.5 0 1 0 0 17a8.5 8.5 0 1 0 0-17z M12 7.2a4.8 4.8 0 1 0 0 9.6a4.8 4.8 0 1 0 0-9.6z M12 3.5v3.7M12 16.8v3.7M3.5 12h3.7M16.8 12h3.7',
  // scroll (βίβλος): the charter and proposals
  charter:
    'M7 4h10.5a2 2 0 0 1 0 4H16v10a3 3 0 0 1-3 3H5.5a2 2 0 0 1 0-4H7z M7 17h6.5a2 2 0 0 1 0 4 M10 8.5h3.5M10 11.5h3.5',
  // owl of Athena (γλαῦξ): the hub's own messages
  hub: 'M5.5 4.5 8 7a6.2 6.2 0 0 1 8 0l2.5-2.5V13a6.5 6.5 0 0 1-13 0z M9.4 8.9a1.7 1.7 0 1 0 0 3.4a1.7 1.7 0 1 0 0-3.4z M14.6 8.9a1.7 1.7 0 1 0 0 3.4a1.7 1.7 0 1 0 0-3.4z M11.2 13.4l.8 1.1.8-1.1 M9 20.8l1-1.6M15 20.8l-1-1.6',
  // amphora (ἀμφορεύς): a project
  project:
    'M9.5 3h5M10.5 3v3.2C7.5 7.3 5.5 10 5.5 13.5c0 4 2.9 7.5 6.5 7.5s6.5-3.5 6.5-7.5c0-3.5-2-6.2-5-7.3V3 M7.4 8.6C5.3 8.4 4.4 10.4 5.6 12M16.6 8.6c2.1-.2 3 1.8 1.8 3.4 M8 14.5h8',
  // wax tablet (δέλτος): a pull request
  pr: 'M4.5 5h15a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1h-15a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1z M12 5v14 M6 9h3.5M6 12h3.5M14.5 9h3.5M14.5 12h2',
  // laurel (δάφνη): CI green
  green:
    'M9 20C4.5 17.5 3 12 5.5 6.5M15 20c4.5-2.5 6-8 3.5-13.5 M5.5 6.5 4 5.2M4.5 10.2l-2-.6M4.6 13.8l-2 .5M6.4 17.3 5 18.8M18.5 6.5 20 5.2M19.5 10.2l2-.6M19.4 13.8l2 .5M17.6 17.3l1.4 1.5',
  // ostrakon (ὄστρακον): CI red or a conflict
  red: 'M5 7.5 13.5 4 19.5 9 16.8 19 7.2 20.5 4 13z M9 10.5l2.2 3.5M11.2 10.5 9 14M13.5 11v4',
} as const;

type IconName = keyof typeof paths;

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
