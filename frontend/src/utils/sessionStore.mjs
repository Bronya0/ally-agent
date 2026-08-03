const DB_NAME = 'ally-session-store';
const DB_VERSION = 2;
const STORE_NAME = 'sessions';

let openPromise;

export function createSerialWriteQueue() {
  let tail = Promise.resolve();
  return {
    enqueue(operation) {
      const result = tail.catch(() => {}).then(operation);
      tail = result.catch(() => {});
      return result;
    },
    flush() {
      return tail;
    },
  };
}

const writes = createSerialWriteQueue();

function indexedDBAvailable() {
  return typeof indexedDB !== 'undefined';
}

function openDatabase() {
  if (!indexedDBAvailable()) return Promise.resolve(null);
  if (openPromise) return openPromise;
  openPromise = new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    request.onerror = () => {
      openPromise = null;
      reject(request.error || new Error('IndexedDB open failed'));
    };
    request.onblocked = () => {
      openPromise = null;
      reject(new Error('IndexedDB upgrade blocked by another Ally window'));
    };
    request.onupgradeneeded = () => {
      const db = request.result;
      if (db.objectStoreNames.contains('snapshots')) db.deleteObjectStore('snapshots');
      if (!db.objectStoreNames.contains(STORE_NAME)) db.createObjectStore(STORE_NAME, { keyPath: 'id' });
    };
    request.onsuccess = () => {
      const db = request.result;
      db.onversionchange = () => {
        db.close();
        openPromise = null;
      };
      resolve(db);
    };
  });
  return openPromise;
}

async function transactionRequest(mode, operation) {
  const db = await openDatabase();
  if (!db) return null;
  return new Promise((resolve, reject) => {
    let request;
    try {
      const transaction = db.transaction(STORE_NAME, mode);
      request = operation(transaction.objectStore(STORE_NAME));
      request.onerror = () => reject(request.error || new Error('IndexedDB request failed'));
      transaction.onabort = () => reject(transaction.error || new Error('IndexedDB transaction aborted'));
      transaction.onerror = () => reject(transaction.error || new Error('IndexedDB transaction failed'));
      transaction.oncomplete = () => resolve(request?.result ?? null);
    } catch (error) {
      reject(error);
    }
  });
}

export async function loadSessionSnapshots() {
  const result = await transactionRequest('readonly', (store) => store.getAll());
  return Array.isArray(result) ? result : [];
}

export function clearSessionSnapshotStore() {
  return writes.enqueue(() => transactionRequest('readwrite', (store) => store.clear()));
}
