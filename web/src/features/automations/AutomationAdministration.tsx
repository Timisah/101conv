import { RefreshCw } from 'lucide-react';
import { PageHeading } from '../../components/common';
import '../admin/admin.css';
import type { Preset, Profile } from '../converters/types';
import { useData } from '../useData';
import { AutomationsSection } from './AutomationsSection';
import { DrivesSection } from './DrivesSection';
import type { AutomationView, BlueprintView, DriveView, ExtensionView } from './types';

export function AutomationAdministration({
  view,
  notify,
}: {
  view: 'drives' | 'automations';
  notify: (message: string) => void;
}) {
  const drives = useData<{ drives: DriveView[] }>('/drives', { drives: [] });
  const automations = useData<{ automations: AutomationView[] }>('/automations', { automations: [] });
  const blueprints = useData<{ blueprints: BlueprintView[] }>('/blueprints', { blueprints: [] });
  const profileData = useData<{ profiles: Profile[] }>('/profiles', { profiles: [] });
  const presetData = useData<{ presets: Preset[] }>('/presets', { presets: [] });
  const extensionData = useData<{ extensions: ExtensionView[] }>('/extensions', { extensions: [] });

  async function reload() {
    await Promise.all([
      drives.refresh(),
      automations.refresh(),
      blueprints.refresh(),
      profileData.refresh(),
      presetData.refresh(),
      extensionData.refresh(),
    ]);
  }

  async function refreshExtensionsAndAutomations() {
    await extensionData.refresh();
    await automations.refresh();
  }

  return (
    <>
      <PageHeading
        title={view === 'drives' ? 'Mounted drives' : 'Automations'}
        description={
          view === 'drives'
            ? 'Register folders already mounted into Core. Docker and Portainer remain responsible for host mounts.'
            : 'Build a typed visual flow from a blueprint, preview matching files, then enable it.'
        }
        action={
          <button className="secondary" onClick={() => void reload()}>
            <RefreshCw size={15} /> Refresh
          </button>
        }
      />
      {view === 'drives' ? (
        <DrivesSection
          drives={drives.data.drives || []}
          loading={drives.loading}
          error={drives.error}
          onRefresh={drives.refresh}
          notify={notify}
        />
      ) : (
        <AutomationsSection
          drives={drives.data.drives || []}
          automations={automations.data.automations || []}
          automationsLoading={automations.loading}
          automationsError={automations.error}
          onRefreshAutomations={automations.refresh}
          blueprints={blueprints.data.blueprints || []}
          profiles={profileData.data.profiles || []}
          presets={presetData.data.presets || []}
          extensions={extensionData.data.extensions || []}
          extensionsError={extensionData.error}
          onRefreshExtensionsAndAutomations={refreshExtensionsAndAutomations}
          notify={notify}
        />
      )}
    </>
  );
}
