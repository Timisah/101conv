// Small formatting and message helpers shared by feature modules.
export const json=(value:unknown)=>JSON.stringify(value);
export const bytes=(size:number)=>size<1024*1024?(size/1024).toFixed(0)+' KB':(size/1024/1024).toFixed(1)+' MB';
export const pretty=(value:string)=>value.charAt(0).toUpperCase()+value.slice(1);
export const explain=(text?:string)=>!text?'Something went wrong. Please try again.':/unsupported|not support/i.test(text)?'This profile cannot convert that file type. Choose another compatible format.':/engine|dependency|capability/i.test(text)?'A required converter is missing or incompatible. Check Converter management.':/timeout|deadline/i.test(text)?'The conversion took too long. Try a smaller file or another profile.':/insufficient.*storage|too little free|no space left/i.test(text)?'Not enough free storage for this file. An administrator can check Storage for available space.':text;
export function fileStatus(state?:string){return ({available:'available',scheduled:'scheduled for cleanup',expired:'expired',missing:'missing unexpectedly',not_ready:'not ready'} as Record<string,string>)[state||'']||'status unavailable';}
