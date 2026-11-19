import { useState, type FormEvent } from 'react';
import { FolderOpen } from 'lucide-react';
import { ErrorState, Loading } from '../../components/common';
import { json } from '../format';
import { apiRequest } from '../session';
import type { DriveView } from './types';

export function DrivesSection({
  drives,
  loading,
  error,
  onRefresh,
  notify,
}: {
  drives: DriveView[];
  loading: boolean;
  error: string;
  onRefresh: () => Promise<void>;
  notify: (message: string) => void;
}) {
  const [driveName, setDriveName] = useState('');
  const [drivePath, setDrivePath] = useState('');
  const [driveAccess, setDriveAccess] = useState<'read' | 'read_write'>('read');
  const [editingDrive, setEditingDrive] = useState<DriveView | null>(null);
  const [testPath, setTestPath] = useState('');
  const [testWrite, setTestWrite] = useState(false);
  const [busy, setBusy] = useState(false);

  async function submitDrive(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    try {
      const body = {
        name: driveName,
        path: drivePath,
        access: driveAccess,
        enabled: editingDrive?.enabled ?? true,
      };
      await apiRequest(editingDrive ? '/drives/' + editingDrive.id : '/drives', {
        method: editingDrive ? 'PUT' : 'POST',
        body: json(body),
      });
      setDriveName('');
      setDrivePath('');
      setEditingDrive(null);
      await onRefresh();
      notify('Mounted drive saved.');
    } catch (err) {
      notify((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function toggleDrive(drive: DriveView) {
    try {
      await apiRequest('/drives/' + drive.id, {
        method: 'PUT',
        body: json({
          name: drive.name,
          path: drive.path,
          access: drive.access,
          enabled: !drive.enabled,
        }),
      });
      await onRefresh();
      notify(
        drive.enabled
          ? 'Mounted drive disabled. Referencing workflows will pause.'
          : 'Mounted drive enabled.',
      );
    } catch (err) {
      notify((err as Error).message);
    }
  }

  async function testMountedPath() {
    setBusy(true);
    try {
      await apiRequest('/drives/test', {
        method: 'POST',
        body: json({ path: testPath, write: testWrite }),
      });
      notify(testWrite ? 'Read/write access confirmed.' : 'Read access confirmed.');
    } catch (err) {
      notify((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function removeDrive(drive: DriveView) {
    try {
      await apiRequest('/drives/' + drive.id, { method: 'DELETE' });
      await onRefresh();
    } catch (err) {
      notify((err as Error).message);
    }
  }

  return (
    <div className="automation-admin-grid">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h2>Registered locations</h2>
            <p>Paths must be beneath an administrator-configured AUTOMATION_MOUNT_ROOTS directory.</p>
          </div>
          <FolderOpen size={18} />
        </div>
        {error ? (
          <ErrorState message={error} retry={onRefresh} />
        ) : loading ? (
          <Loading />
        ) : drives.length === 0 ? (
          <div className="empty-state">
            <h3>No mounted drives registered</h3>
            <p>Mount a host folder through Docker or Portainer, then register its container path here.</p>
          </div>
        ) : (
          drives.map(drive => (
            <article className="drive-card" key={drive.id}>
              <div className="drive-card-title">
                <FolderOpen size={17} />
                <div>
                  <b>{drive.name}</b>
                  <code>{drive.path}</code>
                </div>
                <span className={'state-pill ' + (drive.enabled && drive.accessible ? 'succeeded' : 'failed')}>
                  {drive.enabled ? (drive.accessible ? 'Accessible' : 'Unavailable') : 'Disabled'}
                </span>
              </div>
              <div className="drive-meta">
                <span>{drive.access === 'read_write' ? 'Read / write' : 'Read only'}</span>
                <span>
                  {drive.freeBytes
                    ? `${(drive.freeBytes / 1073741824).toFixed(1)} GiB free`
                    : 'Free space unavailable'}
                </span>
                <span>{drive.automationCount} automation references</span>
              </div>
              {drive.healthError && <p className="form-error">{drive.healthError}</p>}
              <div className="drive-actions">
                <button
                  className="text-btn"
                  onClick={() => {
                    setEditingDrive(drive);
                    setDriveName(drive.name);
                    setDrivePath(drive.path);
                    setDriveAccess(drive.access);
                  }}
                >
                  Edit
                </button>
                <button className="text-btn" onClick={() => void toggleDrive(drive)}>
                  {drive.enabled ? 'Disable' : 'Enable'}
                </button>
                <button
                  className="text-btn"
                  disabled={drive.automationCount > 0}
                  title={drive.automationCount ? 'Remove or revise referencing automations first' : ''}
                  onClick={() => void removeDrive(drive)}
                >
                  Remove
                </button>
              </div>
            </article>
          ))
        )}
      </section>

      <section className="panel">
        <h2>{editingDrive ? 'Edit mounted drive' : 'Register a drive'}</h2>
        <p className="muted-copy">
          Enter the path inside the Core container. A browser path or host-only path is not visible to Core.
        </p>
        <form className="automation-form" onSubmit={submitDrive}>
          <label className="field-label">
            Name
            <input
              required
              maxLength={100}
              value={driveName}
              onChange={event => setDriveName(event.target.value)}
              placeholder="Incoming photos"
            />
          </label>
          <label className="field-label">
            Container path
            <input
              required
              value={drivePath}
              onChange={event => setDrivePath(event.target.value)}
              placeholder="/mounts/photos"
            />
          </label>
          <label className="field-label">
            Access
            <select
              aria-label="Access"
              value={driveAccess}
              onChange={event => setDriveAccess(event.target.value as 'read' | 'read_write')}
            >
              <option value="read">Read only input</option>
              <option value="read_write">Read and write destination</option>
            </select>
          </label>
          <div className="form-actions">
            <button className="primary" disabled={busy}>
              {editingDrive ? 'Save drive' : 'Register drive'}
            </button>
            {editingDrive && (
              <button
                className="secondary"
                type="button"
                onClick={() => {
                  setEditingDrive(null);
                  setDriveName('');
                  setDrivePath('');
                }}
              >
                Cancel
              </button>
            )}
          </div>
        </form>
        <div className="drive-test">
          <h3>Test path access</h3>
          <input
            aria-label="Path to test"
            value={testPath}
            onChange={event => setTestPath(event.target.value)}
            placeholder="/mounts/photos"
          />
          <label>
            <input
              type="checkbox"
              checked={testWrite}
              onChange={event => setTestWrite(event.target.checked)}
            />{' '}
            Test writes using a temporary probe
          </label>
          <button className="secondary" disabled={!testPath || busy} onClick={() => void testMountedPath()}>
            Test access
          </button>
        </div>
      </section>
    </div>
  );
}
