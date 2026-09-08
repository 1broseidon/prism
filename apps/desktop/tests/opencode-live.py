"""Manual integration: real OpenCode V1 tool execution against a local model fixture. No account/API key required."""
import json,os,pathlib,subprocess,tempfile,threading,time,shutil
from http.server import ThreadingHTTPServer,BaseHTTPRequestHandler
source=(pathlib.Path(__file__).resolve().parents[1]/'src-tauri/src/observers/opencode.js').read_text()
observed=[]; requests=[]
class Handler(BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_POST(self):
  body=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
  if self.path.startswith('/hooks/'):
   observed.append(body);self.send_response(200);self.end_headers();self.wfile.write(b'{}');return
  requests.append(self.path)
  results=[m for m in body.get('messages',[]) if m.get('role')=='tool']
  calls=[('bash',{'command':'printf PRISM_OBSERVER_OK','description':'Print fixture marker'}),('write',{'filePath':str(work/'probe.txt'),'content':'private-fixture-content'}),('read',{'filePath':str(work/'probe.txt')})]
  step=len(results)
  delta={'role':'assistant','content':'Verified.'};finish='stop'
  if body.get('tools') and step<len(calls):
   name,args=calls[step];delta={'role':'assistant','tool_calls':[{'index':0,'id':'fixture-'+str(step),'type':'function','function':{'name':name,'arguments':json.dumps(args)}}]};finish='tool_calls'
  self.send_response(200);self.send_header('Content-Type','text/event-stream' if body.get('stream') else 'application/json');self.end_headers()
  if body.get('stream'):
   for d,f in [(delta,None),({},finish)]:
    chunk={'id':'fixture','object':'chat.completion.chunk','created':int(time.time()),'model':'fixture','choices':[{'index':0,'delta':d,'finish_reason':f}]}
    self.wfile.write(('data: '+json.dumps(chunk)+'\n\n').encode())
   self.wfile.write(b'data: [DONE]\n\n')
  else:
   for call in delta.get('tool_calls',[]):call.pop('index',None)
   self.wfile.write(json.dumps({'id':'fixture','object':'chat.completion','model':'fixture','choices':[{'index':0,'message':delta,'finish_reason':finish}],'usage':{'prompt_tokens':1,'completion_tokens':1,'total_tokens':2}}).encode())
with tempfile.TemporaryDirectory(prefix='prism-opencode-live-') as folder:
 home=pathlib.Path(folder);work=home/'work';work.mkdir();cfg=home/'.config/opencode';(cfg/'plugins').mkdir(parents=True)
 server=ThreadingHTTPServer(('127.0.0.1',0),Handler);threading.Thread(target=server.serve_forever,daemon=True).start();origin='http://127.0.0.1:'+str(server.server_port)
 (cfg/'plugins/prism.js').write_text(source.replace('__PRISM_HOOK_URL__',origin+'/hooks/opencode/fixture'))
 (cfg/'opencode.json').write_text(json.dumps({'provider':{'fixture':{'npm':'@ai-sdk/openai-compatible','options':{'baseURL':origin+'/v1','apiKey':'fixture'},'models':{'test':{'name':'Local fixture','limit':{'context':32000,'output':4096}}}}},'model':'fixture/test','small_model':'fixture/test','permission':{'bash':'allow','edit':'allow','read':'allow'}}))
 env=os.environ.copy();env.update(PWD=str(work),HOME=folder,XDG_CONFIG_HOME=str(home/'.config'),XDG_DATA_HOME=str(home/'.data'),XDG_CACHE_HOME=str(home/'.cache'),XDG_STATE_HOME=str(home/'.state'))
 for key in ['OPENCODE_CONFIG','OPENCODE_CONFIG_DIR','OPENCODE_CONFIG_CONTENT']:env.pop(key,None)
 try:
  result=subprocess.run([shutil.which('opencode') or 'opencode','run','--format','json','--model','fixture/test','Run the fixture tools.'],cwd=work,env=env,capture_output=True,text=True,timeout=50)
  print(json.dumps({'exit':result.returncode,'model_requests':len(requests),'observed_tools':[e.get('tool_name') for e in observed],'identities_present':all(e.get('session_id') and e.get('tool_use_id') for e in observed),'cwd_correct':all(e.get('cwd')==str(work) for e in observed),'file_content_omitted':all('private-fixture-content' not in json.dumps(e) for e in observed),'file_written':(work/'probe.txt').exists()}))
  assert result.returncode == 0, 'OpenCode exited unsuccessfully'
  assert [e.get('tool_name') for e in observed] == ['bash','write','read'], 'Missing or duplicate tool observations'
  assert all(e.get('session_id') and e.get('tool_use_id') and e.get('cwd') == str(work) for e in observed)
  assert all('private-fixture-content' not in json.dumps(e) for e in observed)
  assert (work/'probe.txt').read_text() == 'private-fixture-content'
 except subprocess.TimeoutExpired as e: raise AssertionError('OpenCode fixture timed out') from e
 finally:server.shutdown();server.server_close()
