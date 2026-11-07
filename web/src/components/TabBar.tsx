import { Icon, type IconName } from './Icon';

interface Props<T extends string> {
  id: string;
  label: string;
  tabs: ReadonlyArray<{ value: T; label: string; icon?: IconName; disabled?: boolean }>;
  active: T;
  onChange: (value: T) => void;
}

export function TabBar<T extends string>({ id, label, tabs, active, onChange }: Props<T>) {
  const enabled = tabs.filter((tab) => !tab.disabled);
  return (
    <div className="tabs" role="tablist" aria-label={label}>
      {tabs.map((tab) => (
        <button
          type="button"
          role="tab"
          id={`${id}-tab-${tab.value}`}
          key={tab.value}
          className={`tab ${active === tab.value ? 'active' : ''}`}
          aria-selected={active === tab.value}
          aria-controls={`${id}-panel`}
          tabIndex={active === tab.value ? 0 : -1}
          disabled={tab.disabled}
          onClick={() => onChange(tab.value)}
          onKeyDown={(event) => {
            const index = enabled.findIndex((item) => item.value === tab.value);
            let next: number;
            if (event.key === 'ArrowRight') next = (index + 1) % enabled.length;
            else if (event.key === 'ArrowLeft') next = (index - 1 + enabled.length) % enabled.length;
            else if (event.key === 'Home') next = 0;
            else if (event.key === 'End') next = enabled.length - 1;
            else return;
            event.preventDefault();
            onChange(enabled[next].value);
            document.getElementById(`${id}-tab-${enabled[next].value}`)?.focus();
          }}
        >
          {tab.icon && <Icon name={tab.icon} />}
          {tab.label}
        </button>
      ))}
    </div>
  );
}
