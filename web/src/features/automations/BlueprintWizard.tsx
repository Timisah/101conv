import { useState } from 'react';
import type { BlueprintView } from './types';
import './BlueprintWizard.css';

const CATEGORIES = ['all', 'general', 'images', 'video', 'documents'] as const;

export function BlueprintWizard({
  blueprints,
  selectedId,
  onSelect,
}: {
  blueprints: BlueprintView[];
  selectedId: string;
  onSelect: (blueprint: BlueprintView) => void;
}) {
  const [category, setCategory] = useState<(typeof CATEGORIES)[number]>('all');
  const visible = blueprints.filter(
    item => category === 'all' || (item.category || 'general') === category,
  );

  if (blueprints.length <= 2) {
    return null;
  }

  return (
    <div className="blueprint-wizard" aria-label="Quick-start blueprint templates">
      <div className="blueprint-wizard-head">
        <strong>Quick-start templates</strong>
        <div className="blueprint-category-pills" role="tablist" aria-label="Template categories">
          {CATEGORIES.map(cat => (
            <button
              key={cat}
              type="button"
              role="tab"
              aria-selected={category === cat}
              className={'chip-btn' + (category === cat ? ' active' : '')}
              onClick={() => setCategory(cat)}
            >
              {cat === 'all' ? 'All' : cat.charAt(0).toUpperCase() + cat.slice(1)}
            </button>
          ))}
        </div>
      </div>
      <div className="blueprint-card-grid">
        {visible.map(bp => {
          const active = bp.id === selectedId;
          return (
            <button
              key={bp.id}
              type="button"
              className={'blueprint-card' + (active ? ' selected' : '')}
              aria-pressed={active}
              onClick={() => onSelect(bp)}
            >
              <div className="blueprint-card-title">
                <b>{bp.name}</b>
                <span className="state-pill ready">{bp.mode}</span>
              </div>
              <small>{bp.description}</small>
            </button>
          );
        })}
      </div>
    </div>
  );
}
