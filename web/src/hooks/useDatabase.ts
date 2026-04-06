import { useState, useEffect, useCallback } from 'react';
import { api, type DBInfo, type TableInfo, type StatsInfo } from '../api/client';

export function useDatabases() {
  const [databases, setDatabases] = useState<DBInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const dbs = await api.listDatabases();
      setDatabases(dbs || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load databases');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);

  return { databases, loading, error, refresh };
}

export function useSchema(dbName: string | null) {
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!dbName) {
      setTables([]);
      return;
    }
    try {
      setLoading(true);
      setError(null);
      const schema = await api.getSchema(dbName);
      setTables(schema || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load schema');
    } finally {
      setLoading(false);
    }
  }, [dbName]);

  useEffect(() => { refresh(); }, [refresh]);

  return { tables, loading, error, refresh };
}

export function useStats() {
  const [stats, setStats] = useState<StatsInfo | null>(null);

  useEffect(() => {
    const load = async () => {
      try {
        setStats(await api.getStats());
      } catch { /* ignore */ }
    };
    load();
    const interval = setInterval(load, 5000);
    return () => clearInterval(interval);
  }, []);

  return stats;
}
