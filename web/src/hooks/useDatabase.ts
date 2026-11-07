import { useState, useEffect, useCallback, useRef } from 'react';
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
  const [schema, setSchema] = useState<{
    dbName: string | null;
    tables: TableInfo[];
    loading: boolean;
    error: string | null;
  }>({ dbName: null, tables: [], loading: false, error: null });
  const generation = useRef(0);
  const cancelPending = useCallback(() => { generation.current++; }, []);

  const refresh = useCallback(async () => {
    const request = ++generation.current;
    if (!dbName) {
      setSchema({ dbName: null, tables: [], loading: false, error: null });
      return;
    }
    setSchema((current) => ({
      dbName,
      tables: current.dbName === dbName ? current.tables : [],
      loading: true,
      error: null,
    }));
    try {
      const tables = await api.getSchema(dbName);
      if (request === generation.current) {
        setSchema({ dbName, tables: tables || [], loading: false, error: null });
      }
    } catch (err) {
      if (request === generation.current) {
        setSchema({ dbName, tables: [], loading: false, error: err instanceof Error ? err.message : 'Failed to load schema' });
      }
    }
  }, [dbName]);

  useEffect(() => {
    void refresh();
    return cancelPending;
  }, [refresh, cancelPending]);

  return {
    tables: schema.dbName === dbName ? schema.tables : [],
    loading: schema.dbName !== dbName || schema.loading,
    error: schema.dbName === dbName ? schema.error : null,
    refresh,
  };
}

export function useStats() {
  const [stats, setStats] = useState<StatsInfo | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    const load = async () => {
      try {
        const next = await api.getStats();
        if (active) {
          setStats(next);
          setError(null);
        }
      } catch (err) {
        if (active) {
          setError(err instanceof Error ? err.message : 'Unable to connect to daemon');
        }
      }
    };
    void load();
    const interval = setInterval(load, 5000);
    return () => { active = false; clearInterval(interval); };
  }, []);

  return { stats, error };
}
