export function ValidationMessage({message}:{message:string}) {
 return <p className="field-validation-error" role="status">{message}</p>
}
