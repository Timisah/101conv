// Small formatting and message helpers shared by feature modules.
export const json=(value:unknown)=>JSON.stringify(value);
// Binary units, scaled so large capacities read as "28.8 GB" rather than "29447.7 MB".
export const bytes=(size:number)=>{
  const mb=size/1024/1024;
  if(mb<1)return (size/1024).toFixed(0)+' KB';
  if(mb<1024)return mb.toFixed(1)+' MB';
  const gb=mb/1024;
  if(gb<1024)return gb.toFixed(gb<10?2:1)+' GB';
  return (gb/1024).toFixed(2)+' TB';
};
export const pretty=(value:string)=>{const text=value.replace(/_/g,' ');return text.charAt(0).toUpperCase()+text.slice(1);};
export const explain=(text?:string)=>!text?'Something went wrong. Please try again.':/unsupported|not support/i.test(text)?'This profile cannot convert that file type. Choose another compatible format.':/engine|dependency|capability/i.test(text)?'A required converter is missing or incompatible. Check Converter management.':/timeout|deadline/i.test(text)?'The conversion took too long. Try a smaller file or another profile.':/insufficient.*storage|too little free|no space left/i.test(text)?'Not enough free storage for this file. An administrator can check Storage for available space.':text;
export function fileStatus(state?:string){return ({available:'available',scheduled:'scheduled for cleanup',expired:'expired',missing:'missing unexpectedly',not_ready:'not ready'} as Record<string,string>)[state||'']||'status unavailable';}
