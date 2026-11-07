// SPDX-License-Identifier: GPL-3.0-only
// Run with node --experimental-vm-modules --test desktop/pluginhost/plugin_lifecycle_test.mjs
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve=yes; reject=no; });
  return {promise, resolve, reject};
};
const turn = () => new Promise(resolve => setImmediate(resolve));
async function hostModule(mount) {
  let stops=0;
  const context=vm.createContext({console, window:{pluginConfig:{token:'test'}}});
  const theme=new vm.SyntheticModule(['themeSnapshot','onThemeChange'], function() {
    this.setExport('themeSnapshot', () => ({tokens:{}}));
    this.setExport('onThemeChange', () => () => {stops++;});
  }, {context});
  const paths=new vm.SyntheticModule(['pluginWorkspacePath'], function() {
    this.setExport('pluginWorkspacePath', value => value);
  }, {context});
  const entry=new vm.SyntheticModule(['mount'], function() {this.setExport('mount', mount);}, {context});
  await entry.link(() => {}); await entry.evaluate();
  const source=await readFile(new URL('../../resources/plugin/pluginHost.mjs', import.meta.url),'utf8');
  const module=new vm.SourceTextModule(source, {context, importModuleDynamically:() => entry});
  await module.link(name => name.includes('Theme') ? theme : paths);
  await module.evaluate();
  return {mountPlugin:module.namespace.mountPlugin, stops:() => stops};
}

test('dispose is one promise and waits for asynchronous unmount before clearing', async () => {
  const finish=deferred(); let unmounts=0, hidden=0;
  const mod=await hostModule((element,host) => {
    host.events.on('hidden', () => {hidden++;});
    return async () => {unmounts++; await finish.promise;};
  });
  const element={innerHTML:'mounted'};
  const handle=await mod.mountPlugin({id:'demo',entry:'index.js'},element,{},{});
  const first=handle.dispose();
  assert.equal(first,handle.dispose());
  await turn();
  assert.equal(unmounts,1); assert.equal(hidden,1);
  assert.equal(element.innerHTML,'mounted'); assert.equal(mod.stops(),0);
  handle.setActive(true);
  finish.resolve(); await first;
  assert.equal(element.innerHTML,''); assert.equal(mod.stops(),1);
  assert.equal(first,handle.dispose());
  const hostCleanup=handle.host.__host.dispose();
  assert.equal(hostCleanup,handle.host.__host.dispose());
  await hostCleanup; assert.equal(mod.stops(),1);
});

test('rejected asynchronous unmount still releases host subscriptions and container', async () => {
  const mod=await hostModule(() => async () => {throw new Error('unmount failed');});
  const element={innerHTML:'mounted'};
  const handle=await mod.mountPlugin({id:'demo',entry:'index.js'},element,{},{});
  const first=handle.dispose();
  await assert.rejects(first,/unmount failed/);
  assert.equal(first,handle.dispose());
  assert.equal(element.innerHTML,''); assert.equal(mod.stops(),1);
});

test('allyDispose waits for in-flight mount and then its cleanup', async () => {
  const mounted=deferred(), cleaned=deferred(); let disposals=0, closed=0, shown=0;
  const element={textContent:''};
  const context=vm.createContext({console, pluginConfig:{token:'x',state:{active:true,tokens:{}},env:{},plugin:{}},
    document:{documentElement:{style:{setProperty(){}},setAttribute(){}},getElementById:() => element},
    window:{addEventListener(){}}, EventSource:class {close(){closed++;}}});
  const dependency=new vm.SyntheticModule(['mountPlugin','notifyNative'],function() {
    this.setExport('mountPlugin',() => mounted.promise); this.setExport('notifyNative',() => {});
  },{context});
  const html=await readFile(new URL('../../resources/plugin/index.html',import.meta.url),'utf8');
  const script=html.split('<script type="module">')[1].split('</script>')[0];
  const module=new vm.SourceTextModule(script,{context});
  await module.link(() => dependency);
  const evaluating=module.evaluate();
  const first=context.window.allyDispose();
  assert.equal(first,context.window.allyDispose()); assert.equal(closed,1);
  let done=false; first.then(() => {done=true;}); await turn(); assert.equal(done,false);
  mounted.resolve({dispose:() => {disposals++;return cleaned.promise;},setActive:() => {shown++;}});
  await evaluating; await turn();
  assert.equal(disposals,1); assert.equal(shown,0); assert.equal(done,false);
  cleaned.resolve(); await first; assert.equal(done,true);
});
