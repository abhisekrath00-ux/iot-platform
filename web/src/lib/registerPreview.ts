import {MapPoint} from './registerMap';
export interface RegisterLabel {include:boolean;name:string;id:string;type:string;order:string;scale:string;unit:string;min:string;max:string}
export const newRegisterLabel=():RegisterLabel=>({include:false,name:'',id:'',type:'',order:'',scale:'1',unit:'',min:'',max:''});
export const registerWidth=(type:string)=>['u32','i32','f32'].includes(type)?2:1;
// Mirrors the edge Modbus decoder. Input is unscaled 16-bit wire words, never
// guessed physical meanings. A 32-bit point requires its adjacent returned word.
export function decodeRegister(words:number[],index:number,type:string,order:string):number {
 if(!['u16','i16','u32','i32','f32','bool'].includes(type)||!['abcd','badc','cdab','dcba'].includes(order))return NaN;
 let part=words.slice(index,index+registerWidth(type));if(part.length!==registerWidth(type)||part.some(v=>!Number.isInteger(v)||v<0||v>65535))return NaN;
 if(type==='bool')return part[0]===0||part[0]===1?part[0]:NaN;
 if(order==='cdab'||order==='dcba')part=part.reverse();
 const bytes=part.flatMap(v=>order==='badc'||order==='dcba'?[v&255,v>>8]:[v>>8,v&255]);const data=new DataView(new Uint8Array(bytes).buffer);
 switch(type){case 'u16':return data.getUint16(0);case 'i16':return data.getInt16(0);case 'u32':return data.getUint32(0);case 'i32':return data.getInt32(0);case 'f32':return data.getFloat32(0);default:return NaN;}
}
export function labelledPoint(label:RegisterLabel,register:number,func:number):MapPoint {
 return {name:label.name.trim(),id:label.id.trim(),register,func,type:label.type,word_order:label.order,scale:label.scale.trim()===''?NaN:Number(label.scale),unit:label.unit.trim(),min:label.min.trim()===''?NaN:Number(label.min),max:label.max.trim()===''?NaN:Number(label.max)};
}
